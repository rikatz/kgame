/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package gatewayclass

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
	options   GatewayClassOptions
}

// AddFinalizerFunc is a function that should be called immediately before adding a
// finalizer.
// If empty the finalizer will be added without further check
type AddFinalizerFunc func(ctx context.Context) error

// RemoveFinalizerFunc is a function that should be called immediately before removing
// a finalizer. If empty the finalizer will be removed without any further check
type RemoveFinalizerFunc func(ctx context.Context) error

type GatewayClassOptions struct {
	FinalizerName       string
	AddFinalizerFunc    AddFinalizerFunc
	RemoveFinalizerFunc RemoveFinalizerFunc
}

// SetupWithManager sets the GatewayClass controller to be started with the current
// manager
// We don't add any predicate here to check the GatewayClass, because we drop the
// undesired GatewayClass already on controller-runtime cache level (see tunables)
func SetupWithManager(mgr manager.Manager, options GatewayClassOptions) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1.GatewayClass{}).
		Complete(&reconciler{
			options: options,
			client:  mgr.GetClient(),
			scheme:  mgr.GetScheme(),
			logger:  mgr.GetLogger().WithValues("controller", "gatewayclass"),
		})
}

// Reconcile executes the reconciliation process of this GatewayClass
func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := r.logger.WithValues("name", req.Name)
	logger.Info("reconciling")

	gatewayClass := gatewayv1.GatewayClass{}
	if err := r.client.Get(ctx, req.NamespacedName, &gatewayClass); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return reconcile.Result{}, nil
		}
		logger.Error(err, "unable to reconcile")
		return reconcile.Result{}, err
	}

	if !gatewayClass.GetDeletionTimestamp().IsZero() {
		if r.options.FinalizerName != "" && controllerutil.RemoveFinalizer(&gatewayClass, r.options.FinalizerName) {
			if r.options.RemoveFinalizerFunc != nil {
				if err := r.options.RemoveFinalizerFunc(ctx); err != nil {
					return reconcile.Result{}, fmt.Errorf("error executing pre-finalizer removal function: %w", err)
				}
			}

			r.logger.Info("removing finalizer", "finalizer", r.options.FinalizerName)
			return reconcile.Result{}, r.client.Update(ctx, &gatewayClass)
		}
	}

	// Normal update, should try to add a finalizer if none exists
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := r.client.Get(ctx, req.NamespacedName, &gatewayClass); err != nil {
			// Could not get GatewayClass (maybe deleted)
			return client.IgnoreNotFound(err)
		}

		if r.options.FinalizerName != "" && controllerutil.AddFinalizer(&gatewayClass, r.options.FinalizerName) {
			if r.options.AddFinalizerFunc != nil {
				if err := r.options.AddFinalizerFunc(ctx); err != nil {
					return fmt.Errorf("error executing pre-finalizer add function: %w", err)
				}
			}
			r.logger.Info("adding finalizer", "finalizer", r.options.FinalizerName)
			return r.client.Update(ctx, &gatewayClass)
		}
		markAsAccepted(gatewayClass.Status.Conditions, gatewayClass.Generation)
		return r.client.Status().Update(ctx, &gatewayClass)
	})

	return reconcile.Result{}, err
}

func markAsAccepted(conditions []metav1.Condition, generation int64) {
	for i, cond := range conditions {
		if cond.Type == string(gatewayv1.GatewayClassConditionStatusAccepted) {
			conditions[i] = metav1.Condition{
				Type:               string(gatewayv1.GatewayClassConditionStatusAccepted),
				Status:             metav1.ConditionTrue,
				Reason:             string(gatewayv1.GatewayClassReasonAccepted),
				Message:            "GatewayClass is accepted",
				LastTransitionTime: metav1.Now(),
				ObservedGeneration: generation,
			}
		}
	}
}
