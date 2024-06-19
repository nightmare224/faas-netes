// Copyright (c) Alex Ellis 2017. All rights reserved.
// Copyright 2020 OpenFaaS Author(s)
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/openfaas/faas-netes/pkg/catalog"
	"github.com/openfaas/faas-provider/types"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// MakeDeleteHandler delete a function
func MakeDeleteHandler(defaultNamespace string, clientset *kubernetes.Clientset, c catalog.Catalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		q := r.URL.Query()
		namespace := q.Get("namespace")

		lookupNamespace := defaultNamespace

		if len(namespace) > 0 {
			lookupNamespace = namespace
		}

		if lookupNamespace == "kube-system" {
			http.Error(w, "unable to list within the kube-system namespace", http.StatusUnauthorized)
			return
		}

		if lookupNamespace != defaultNamespace {
			http.Error(w, fmt.Sprintf("namespace must be: %s", defaultNamespace), http.StatusBadRequest)
			return
		}

		body, _ := io.ReadAll(r.Body)

		request := types.DeleteFunctionRequest{}
		err := json.Unmarshal(body, &request)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if len(request.FunctionName) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		getOpts := metav1.GetOptions{}

		// This makes sure we don't delete non-labelled deployments
		deployment, findDeployErr := clientset.AppsV1().
			Deployments(lookupNamespace).
			Get(context.TODO(), request.FunctionName, getOpts)

		if findDeployErr != nil {
			if errors.IsNotFound(findDeployErr) {
				w.WriteHeader(http.StatusNotFound)
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}

			w.Write([]byte(findDeployErr.Error()))
			return
		}

		if isFunction(deployment) {
			err := deleteFunction(lookupNamespace, clientset, request)
			if err != nil {
				if errors.IsNotFound(err) {
					w.WriteHeader(http.StatusNotFound)
				} else {
					w.WriteHeader(http.StatusInternalServerError)
				}
				w.Write([]byte(err.Error()))
				return
			}
		} else {
			w.WriteHeader(http.StatusBadRequest)

			w.Write([]byte("Not a function: " + request.FunctionName))
			return
		}

		// update the catalog
		// c.DeleteAvailableFunctions(request.FunctionName)

		w.WriteHeader(http.StatusAccepted)
	}
}

func isFunction(deployment *appsv1.Deployment) bool {
	if deployment != nil {
		if _, found := deployment.Labels["faas_function"]; found {
			return true
		}
	}
	return false
}

func deleteFunction(functionNamespace string, clientset *kubernetes.Clientset, request types.DeleteFunctionRequest) error {
	foregroundPolicy := metav1.DeletePropagationForeground
	opts := &metav1.DeleteOptions{PropagationPolicy: &foregroundPolicy}

	if deployErr := clientset.AppsV1().Deployments(functionNamespace).
		Delete(context.TODO(), request.FunctionName, *opts); deployErr != nil {
		log.Println("error deleting function's deployment")
		return deployErr
	}

	if svcErr := clientset.CoreV1().
		Services(functionNamespace).
		Delete(context.TODO(), request.FunctionName, *opts); svcErr != nil {
		fmt.Println("error deleting function's service")
		return svcErr
	}
	return nil
}
