// Copyright (c) Alex Ellis 2017. All rights reserved.
// Copyright 2020 OpenFaaS Author(s)
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/openfaas/faas-netes/pkg/catalog"
)

// MaxReplicas licensed for OpenFaaS CE is 5/5
// a license for OpenFaaS Standard is required to increase this limit.
const MaxReplicas = 5

// MaxFunctions licensed for OpenFaaS CE is 15
// a license for OpenFaaS Standard is required to increase this limit.
const MaxFunctions = 15

// MakeReplicaReader reads the amount of replicas for a deployment
func MakeReplicaReader(defaultNamespace string, c catalog.Catalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		vars := mux.Vars(r)

		functionName := vars["name"]
		q := r.URL.Query()
		namespace := q.Get("namespace")

		lookupNamespace := defaultNamespace

		if len(namespace) > 0 {
			lookupNamespace = namespace
		}

		if lookupNamespace != defaultNamespace {
			http.Error(w, fmt.Sprintf("namespace must be: %s", defaultNamespace), http.StatusBadRequest)
			return
		}

		// s := time.Now()

		// for _, lister := range listers {
		// 	function, err := getService(lookupNamespace, functionName, lister)
		// 	if err != nil {
		// 		log.Printf("Unable to fetch service: %s %s\n", functionName, namespace)
		// 		continue
		// 	}
		// 	// find function, always only list the closetest cluster
		// 	if function != nil {
		// 		d := time.Since(s)
		// 		log.Printf("Replicas: %s.%s, (%d/%d) %dms\n", functionName, lookupNamespace, function.AvailableReplicas, function.Replicas, d.Milliseconds())

		// 		functionBytes, err := json.Marshal(function)
		// 		if err != nil {
		// 			klog.Errorf("Failed to marshal function: %s", err.Error())
		// 			w.WriteHeader(http.StatusInternalServerError)
		// 			w.Write([]byte("Failed to marshal function"))
		// 			return
		// 		}

		// 		w.Header().Set("Content-Type", "application/json")
		// 		w.WriteHeader(http.StatusOK)
		// 		w.Write(functionBytes)
		// 		return
		// 	}
		// }
		// // did not found anything so return 404
		// w.WriteHeader(http.StatusNotFound)
		fname := functionName
		if strings.Contains(functionName, ".") {
			fname = strings.TrimSuffix(functionName, "."+lookupNamespace)
		}
		if fn, err := c.GetAvailableFunction(fname); err == nil {
			//TODO: the available replicas here do not show the replica in the host, but show the
			// overall replica, because if show 0 the gateway would consider it not ready
			// also the first trigger from 0->1 will be extremely slow
			// TODO: maybe fix this in gateway
			fn.AvailableReplicas = max(fn.Replicas, fn.AvailableReplicas)
			functionBytes, _ := json.Marshal(fn)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(functionBytes)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// getService returns a function/service or nil if not found
// func getService(functionNamespace string, functionName string, lister v1.DeploymentLister) (*types.FunctionStatus, error) {

// 	item, err := lister.Deployments(functionNamespace).
// 		Get(functionName)

// 	if err != nil {
// 		if errors.IsNotFound(err) {
// 			return nil, nil
// 		}

// 		return nil, err
// 	}

// 	if item != nil {
// 		function := k8s.AsFunctionStatus(*item)
// 		if function != nil {
// 			return function, nil
// 		}
// 	}

// 	return nil, fmt.Errorf("function: %s not found", functionName)
// }
