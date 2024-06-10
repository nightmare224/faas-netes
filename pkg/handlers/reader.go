// Copyright (c) Alex Ellis 2017. All rights reserved.
// Copyright 2020 OpenFaaS Author(s)
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/openfaas/faas-netes/pkg/catalog"
)

// MakeFunctionReader handler for reading functions deployed in the cluster as deployments.
func MakeFunctionReader(defaultNamespace string, c catalog.Catalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		// q := r.URL.Query()
		// namespace := q.Get("namespace")

		// lookupNamespace := defaultNamespace

		// if len(namespace) > 0 {
		// 	lookupNamespace = namespace
		// }

		// if lookupNamespace != defaultNamespace {
		// 	http.Error(w, fmt.Sprintf("namespace must be: %s", defaultNamespace), http.StatusBadRequest)
		// 	return
		// }

		// if lookupNamespace == "kube-system" {
		// 	http.Error(w, "unable to list within the kube-system namespace", http.StatusUnauthorized)
		// 	return
		// }

		// functions, err := getServiceList(lookupNamespace, deploymentListers)
		// if err != nil {
		// 	log.Println(err)
		// 	w.WriteHeader(http.StatusInternalServerError)
		// 	w.Write([]byte(err.Error()))
		// 	return
		// }

		// functionBytes, err := json.Marshal(functions)
		// if err != nil {
		// 	klog.Errorf("Failed to marshal functions: %s", err.Error())
		// 	w.WriteHeader(http.StatusInternalServerError)
		// 	w.Write([]byte("Failed to marshal functions"))
		// 	return
		// }

		// w.Header().Set("Content-Type", "application/json")
		// w.WriteHeader(http.StatusOK)
		// w.Write(functionBytes)

		infoLevel := catalog.ClusterLevel
		res := c.ListAvailableFunctions(infoLevel)
		body, _ := json.Marshal(res)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}
}

// func getServiceList(functionNamespace string, deploymentListers []v1.DeploymentLister) ([]types.FunctionStatus, error) {
// 	functions := []types.FunctionStatus{}

// 	sel := labels.NewSelector()
// 	req, err := labels.NewRequirement("faas_function", selection.Exists, []string{})
// 	if err != nil {
// 		return functions, err
// 	}
// 	onlyFunctions := sel.Add(*req)

// 	functionnameSet := make(map[string]struct{})
// 	for _, deploymentLister := range deploymentListers {
// 		res, err := deploymentLister.Deployments(functionNamespace).List(onlyFunctions)

// 		if err != nil {
// 			return nil, err
// 		}
// 		for _, item := range res {
// 			if item != nil {
// 				// ignore if the function already is listed by closet cluster
// 				if _, exist := functionnameSet[item.Name]; exist {
// 					continue
// 				}
// 				function := k8s.AsFunctionStatus(*item)
// 				if function != nil {
// 					functions = append(functions, *function)
// 					functionnameSet[item.Name] = struct{}{}
// 				}
// 			}
// 		}
// 	}

// 	return functions, nil
// }
