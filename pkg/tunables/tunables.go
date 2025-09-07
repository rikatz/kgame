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

package tunables

import (
	"github.com/go-logr/logr"
	"k8s.io/client-go/tools/cache"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type tunables struct {
	logger      logr.Logger
	gwClassName gatewayv1.GatewayController
}

type TunableConfig struct {
	Logger           logr.Logger
	GatewayClassName gatewayv1.GatewayController
}

func NewTunables(config TunableConfig) *tunables {
	return &tunables{
		logger:      config.Logger,
		gwClassName: config.GatewayClassName,
	}
}

// TransformGatewayClass is a cache transformation function that should be
// applied to GatewayClass. It will:
// 1. Ignore and drop from the cache a GatewayClass that does not belong to this
// controller
// 2. Strip managedfields from the resource before storing on cache, to save some memory
func (t *tunables) TransformGatewayClass() cache.TransformFunc {
	return func(i any) (any, error) {
		logger := t.logger.WithName("gwclass-transform")
		gwclass, ok := i.(*gatewayv1.GatewayClass)
		if !ok {
			logger.Info("ignoring object as it is not a gateway class")
			return nil, nil
		}
		// Drop the object from cache if we don't care about it
		if gwclass.Spec.ControllerName != t.gwClassName {
			logger.Info("ignoring object with unknown class", "name", gwclass.GetName())
			return nil, nil
		}
		// Clean managed fields for some memory economy
		gwclass.SetManagedFields(nil)
		return gwclass, nil
	}
}
