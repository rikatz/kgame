package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/rikatz/kgame/pkg/controllers/gateway"
	"github.com/rikatz/kgame/pkg/controllers/gatewayclass"
	"github.com/rikatz/kgame/pkg/tunables"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var (
	scheme = runtime.NewScheme()
)

type Controller struct {
	mgr    ctrl.Manager
	logger logr.Logger
}

type ControllerOptions struct {
	ControllerClass     string
	ControllerName      string
	GatewayClassOptions gatewayclass.GatewayClassOptions
	GatewayOptions      gateway.GatewayOptions
}

const (
	defaultNameAndClass = "kgame"
)

func NewController(opts *ControllerOptions) (*Controller, error) {
	if opts == nil {
		return nil, fmt.Errorf("options cannot be null")
	}

	if opts.ControllerClass == "" {
		opts.ControllerClass = defaultNameAndClass
	}

	if opts.ControllerName == "" {
		opts.ControllerName = defaultNameAndClass
	}

	logger := klog.NewKlogr().WithName(opts.ControllerName)
	ctrl.SetLogger(logger)

	if err := v1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add corev1 to scheme: %w", err)
	}

	if err := gatewayv1.Install(scheme); err != nil {
		return nil, fmt.Errorf("failed to add gatewayapiv1 to scheme: %w", err)
	}

	tunablesConfig := tunables.TunableConfig{
		Logger:           logger,
		GatewayClassName: gatewayv1.GatewayController(opts.ControllerClass),
	}

	logger.Info("ControllerClass configured", "class", opts.ControllerClass)

	transformFunc := tunables.NewTunables(tunablesConfig)
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Logger: logger,
		Cache: cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				&gatewayv1.GatewayClass{}: {
					Transform: transformFunc.TransformGatewayClass(),
				},
				&gatewayv1.Gateway{}: {
					Transform: cache.TransformStripManagedFields(),
				},
				&gatewayv1.HTTPRoute{}: {
					Transform: cache.TransformStripManagedFields(),
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("unable to create the manager, please check if the CRDs are installed: %w", err)
	}

	if err := gatewayclass.SetupWithManager(mgr, opts.GatewayClassOptions); err != nil {
		return nil, fmt.Errorf("unable to add gatewayclass controller: %w", err)
	}

	if err := gateway.SetupWithManager(mgr, opts.GatewayOptions); err != nil {
		return nil, fmt.Errorf("unable to add gatewayclass controller: %w", err)
	}

	return &Controller{
		mgr:    mgr,
		logger: logger,
	}, nil
}

func (k *Controller) Start(ctx context.Context) error {
	// This is not ideal, but eventually the caller does not want to control the context
	if ctx == nil {
		ctx = ctrl.SetupSignalHandler()
	}

	// TODO: should we wait for client cache to be populated?

	k.logger.Info("starting the controller")
	return k.mgr.Start(ctx)

}
