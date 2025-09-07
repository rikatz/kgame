package main

import (
	"context"
	"os"

	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rikatz/kgame/pkg/controllers"
	"github.com/rikatz/kgame/pkg/controllers/gateway"
	"github.com/rikatz/kgame/pkg/controllers/gatewayclass"
)

// THIS IS AN EXAMPLE USED FOR TESTS!
func main() {
	ctx := ctrl.SetupSignalHandler()

	addFinalizerFunc := func(ctx context.Context) error {
		klog.Info("I am a finalizer add function")
		return nil
	}

	removeFinalizerFunc := func(ctx context.Context) error {
		klog.Info("I am a finalizer removal function")
		return nil
	}

	opts := &controllers.ControllerOptions{
		ControllerClass: "gateway.mylab.tld/gatewayclass-controller",
		ControllerName:  "something",
		GatewayClassOptions: gatewayclass.GatewayClassOptions{
			FinalizerName:       "gateway.mylab.tld/gatewayclass-finalizer",
			AddFinalizerFunc:    addFinalizerFunc,
			RemoveFinalizerFunc: removeFinalizerFunc,
		},
		GatewayOptions: gateway.GatewayOptions{
			FinalizerName:       "gateway.mylab.tld/gateway-finalizer",
			AddFinalizerFunc:    addFinalizerFunc,
			RemoveFinalizerFunc: removeFinalizerFunc,
		},
	}
	ctr, err := controllers.NewController(opts)
	if err != nil {
		klog.Errorf("unable to create the controller instance: %s", err)
		os.Exit(1)
	}

	if err := ctr.Start(ctx); err != nil {
		klog.Errorf("unable to start the controller: %s", err)
		os.Exit(1)
	}
}
