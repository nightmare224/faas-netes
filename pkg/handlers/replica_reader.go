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
const MaxReplicas = 10

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

		fname := functionName
		if strings.Contains(functionName, ".") {
			fname = strings.TrimSuffix(functionName, "."+lookupNamespace)
		}
		if fn, err := c.GetAvailableFunction(fname); err == nil {
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
