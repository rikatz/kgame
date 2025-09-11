package gateway

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type reconciler struct {
	client  client.Client
	scheme  *runtime.Scheme
	logger  logr.Logger
	options GatewayOptions
}

// AddFinalizerFunc is a function that should be called immediately before adding a
// finalizer.
// If empty the finalizer will be added without further check
type AddFinalizerFunc func(ctx context.Context) error

// RemoveFinalizerFunc is a function that should be called immediately before removing
// a finalizer. If empty the finalizer will be removed without any further check
type RemoveFinalizerFunc func(ctx context.Context) error

type GatewayOptions struct {
	FinalizerName       string
	AddFinalizerFunc    AddFinalizerFunc
	RemoveFinalizerFunc RemoveFinalizerFunc
}

// matchManagedGatewayClass will check the object Gateway Class to define if it should
// be reconciled or not.
// Because this controller already ignores caching any non managed GatewayClass,
// any attempt to Get a gatewayclass that does not exist represents that this is
// a gatewayClass that this controller does not manage, so we don't need to match
// the GatewayClass spec.ControllerName
func matchManagedGatewayClass(kubeclient client.Client, logger logr.Logger) func(obj client.Object) bool {
	return func(obj client.Object) bool {
		gw, ok := obj.(*gatewayv1.Gateway)
		if !ok {
			return false
		}

		gatewayclass := &gatewayv1.GatewayClass{}
		gatewayclass.SetName(string(gw.Spec.GatewayClassName))
		err := kubeclient.Get(context.Background(), client.ObjectKeyFromObject(gatewayclass), gatewayclass)
		if err != nil {
			logger.Info("gatewayclass not managed by this controller", "gatewayclass", gatewayclass.Name, "gateway", obj.GetName(), "namespace", obj.GetNamespace())
			return false
		}
		return true
	}
}

// SetupWithManager sets the Gateway controller to be started with the current
// manager
// This manager will start the following indexers:
//   - GatewayClassName - Will be used by GatewayClass controller to define which
//     Gateway should be reconciled in case of a change
//   - Listeners - Will be used to define if there are conflicts with other Listeners/ListenersSet
//   - Services - Will be used to define if a service created by this reconciler has some state change
func SetupWithManager(mgr manager.Manager, options GatewayOptions) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1.Gateway{},
			builder.WithPredicates(predicate.NewPredicateFuncs(
				matchManagedGatewayClass(
					mgr.GetClient(),
					mgr.GetLogger().WithValues("predicate", "gateway"))))).
		Complete(&reconciler{
			options: options,
			client:  mgr.GetClient(),
			scheme:  mgr.GetScheme(),
			logger:  mgr.GetLogger().WithValues("controller", "gateway"),
		})
}

// Reconcile executes the reconciliation process of this Gateway
func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := r.logger.WithValues("name", req.Name)
	logger.Info("reconciling")

	gateway := gatewayv1.Gateway{}
	if err := r.client.Get(ctx, req.NamespacedName, &gateway); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		logger.Error(err, "unable to reconcile")
		return reconcile.Result{}, err
	}

	originalGw := gateway.DeepCopy()

	if gateway.GetDeletionTimestamp() != nil && !gateway.GetDeletionTimestamp().IsZero() {
		if r.options.FinalizerName != "" && controllerutil.RemoveFinalizer(&gateway, r.options.FinalizerName) {
			if r.options.RemoveFinalizerFunc != nil {
				if err := r.options.RemoveFinalizerFunc(ctx); err != nil {
					return reconcile.Result{}, fmt.Errorf("error executing pre-finalizer removal function: %w", err)
				}
			}

			r.logger.Info("removing finalizer", "finalizer", r.options.FinalizerName)
			return reconcile.Result{}, r.client.Patch(ctx, &gateway, client.MergeFrom(originalGw))
		}
	}

	// Normal update, should try to add a finalizer if none exists
	if r.options.FinalizerName != "" && controllerutil.AddFinalizer(&gateway, r.options.FinalizerName) {
		if r.options.AddFinalizerFunc != nil {
			if err := r.options.AddFinalizerFunc(ctx); err != nil {
				return reconcile.Result{}, fmt.Errorf("error executing pre-finalizer add function: %w", err)
			}
		}

		r.logger.Info("adding finalizer", "finalizer", r.options.FinalizerName)
		if err := r.client.Patch(ctx, &gateway, client.MergeFrom(originalGw)); err != nil {
			return reconcile.Result{}, err
		}
	}

	mutateConditions(gateway.Status.Conditions,
		gatewayv1.GatewayConditionAccepted,
		gatewayv1.GatewayReasonAccepted,
		metav1.ConditionTrue,
		"Gateway is accepted",
		gateway.Generation)

	if err := r.client.Status().Patch(ctx, &gateway, client.MergeFrom(originalGw)); err != nil {
		return reconcile.Result{}, fmt.Errorf("error adding accepted condition on %s: %w", req.String(), err)
	}

	// Call the programming logic of the gateway, then mutate the conditions for programmed
	// TODO: should this be added to a retry on conflict? If something changed probably we
	// want a full loop here
	mutateConditions(gateway.Status.Conditions,
		gatewayv1.GatewayConditionProgrammed,
		gatewayv1.GatewayReasonProgrammed,
		metav1.ConditionTrue,
		"Gateway is programmed",
		gateway.Generation)

	if err := r.client.Status().Patch(ctx, &gateway, client.MergeFrom(originalGw)); err != nil {
		return reconcile.Result{}, fmt.Errorf("error adding programmed condition on %s: %w", req.String(), err)
	}

	return reconcile.Result{}, nil
}

// mutateConditions mutates in place conditions.
func mutateConditions(conditions []metav1.Condition,
	condtype gatewayv1.GatewayConditionType,
	reason gatewayv1.GatewayConditionReason,
	status metav1.ConditionStatus,
	message string,
	generation int64) []metav1.Condition {

	var found bool

	newCondition := metav1.Condition{
		Type:               string(condtype),
		Status:             status,
		Reason:             string(reason),
		Message:            message,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: generation,
	}

	for i := range conditions {
		if conditions[i].Type == string(condtype) {
			conditions[i] = newCondition
			found = true
			break
		}
	}
	if !found {
		conditions = append(conditions, newCondition)
	}
	return conditions
}
