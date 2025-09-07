package gateway

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type reconciler struct {
	className string
	client    client.Client
	scheme    *runtime.Scheme
	logger    logr.Logger
	options   GatewayOptions
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

// SetupWithManager sets the Gateway controller to be started with the current
// manager
// This manager will start the following indexers:
//   - GatewayClassName - Will be used by GatewayClass controller to define which
//     Gateway should be reconciled in case of a change
//   - Listeners - Will be used to define if there are conflicts with other Listeners/ListenersSet
//   - Services - Will be used to define if a service created by this reconciler has some state change
func SetupWithManager(mgr manager.Manager, options GatewayOptions) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1.Gateway{}).
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
		if client.IgnoreNotFound(err) == nil {
			return reconcile.Result{}, nil
		}
		logger.Error(err, "unable to reconcile")
		return reconcile.Result{}, err
	}

	if !gateway.GetDeletionTimestamp().IsZero() {
		if r.options.FinalizerName != "" && controllerutil.RemoveFinalizer(&gateway, r.options.FinalizerName) {
			if r.options.RemoveFinalizerFunc != nil {
				if err := r.options.RemoveFinalizerFunc(ctx); err != nil {
					return reconcile.Result{}, fmt.Errorf("error executing pre-finalizer removal function: %w", err)
				}
			}

			r.logger.Info("removing finalizer", "finalizer", r.options.FinalizerName)
			return reconcile.Result{}, r.client.Update(ctx, &gateway)
		}
	}

	// Normal update, should try to add a finalizer if none exists
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := r.client.Get(ctx, req.NamespacedName, &gateway); err != nil {
			// Could not get Gateway (maybe deleted)
			return client.IgnoreNotFound(err)
		}

		if r.options.FinalizerName != "" && controllerutil.AddFinalizer(&gateway, r.options.FinalizerName) {
			if r.options.AddFinalizerFunc != nil {
				if err := r.options.AddFinalizerFunc(ctx); err != nil {
					return fmt.Errorf("error executing pre-finalizer add function: %w", err)
				}
			}
			r.logger.Info("adding finalizer", "finalizer", r.options.FinalizerName)
			if err := r.client.Update(ctx, &gateway); err != nil {
				return err
			}
		}
		mutateConditions(gateway.Status.Conditions,
			gatewayv1.GatewayConditionAccepted,
			gatewayv1.GatewayReasonAccepted,
			metav1.ConditionTrue,
			"Gateway is accepted",
			gateway.Generation)
		return r.client.Status().Update(ctx, &gateway)
	})
	if err != nil {
		return reconcile.Result{}, err
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
	return reconcile.Result{}, r.client.Status().Update(ctx, &gateway)

}

// TODO: Improve this function to mutate the conditions properly, adding not only
// the accepted but other supported conditions
func mutateConditions(conditions []metav1.Condition,
	condtype gatewayv1.GatewayConditionType,
	reason gatewayv1.GatewayConditionReason,
	status metav1.ConditionStatus,
	message string,
	generation int64) {

	var found bool

	newCondition := metav1.Condition{
		Type:               string(condtype),
		Status:             status,
		Reason:             string(reason),
		Message:            message,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: generation,
	}

	for i, cond := range conditions {
		if cond.Type == string(condtype) {
			conditions[i] = newCondition
			found = true
		}
	}
	if !found {
		conditions = append(conditions, newCondition)
	}
}
