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

	"github.com/gorilla/mux"
	"github.com/openfaas/faas-netes/pkg/catalog"
	"github.com/openfaas/faas-netes/pkg/k8s"
	"github.com/openfaas/faas-provider/types"
	v1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MakeReplicaUpdater updates desired count of replicas
func MakeReplicaUpdater(defaultNamespace string, c catalog.Catalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Println("Update replicas")

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

		req := types.ScaleServiceRequest{}

		if r.Body != nil {
			defer r.Body.Close()
			bytesIn, _ := io.ReadAll(r.Body)
			marshalErr := json.Unmarshal(bytesIn, &req)
			if marshalErr != nil {
				w.WriteHeader(http.StatusBadRequest)
				msg := "Cannot parse request. Please pass valid JSON."
				w.Write([]byte(msg))
				log.Println(msg, marshalErr)
				return
			}
		}

		if req.Replicas == 0 {
			http.Error(w, "replicas cannot be set to 0 in OpenFaaS CE",
				http.StatusBadRequest)
			return
		}

		options := metav1.GetOptions{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Deployment",
				APIVersion: "apps/v1",
			},
		}

		deployment, err := c.NodeCatalog[catalog.GetSelfCatalogKey()].Clientset.AppsV1().Deployments(lookupNamespace).Get(context.TODO(), functionName, options)

		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Unable to lookup function deployment " + functionName))
			log.Println(err)
			return
		}

		fn, fnExist := c.FunctionCatalog[deployment.Name]
		if !fnExist {
			msg := fmt.Sprintf("service %s not found", deployment.Name)
			log.Printf("[Scale] %s\n", msg)
			http.Error(w, msg, http.StatusNotFound)
			return
		}

		oldReplicas := int32(fn.Replicas)
		replicas := int32(req.Replicas)
		if replicas >= MaxReplicas {
			replicas = MaxReplicas
		}

		log.Printf("Set replicas - %s %s, %d/%d\n", functionName, lookupNamespace, replicas, oldReplicas)

		// deployment.Spec.Replicas = &replicas

		// TODO: use anitaffinity to make no two pod on same node
		// no change or second hand scale up request.
		// Currently the faasd can not scale up the number of container, so if it receive the scale up from other, just stay
		if oldReplicas == replicas || isOffloadRequest(r) {
			log.Printf("Scale %s: stay replica %d\n", deployment.Name, replicas)
			w.WriteHeader(http.StatusNoContent)
			return
		} else if oldReplicas < replicas { //scale up
			log.Printf("Scale up %s: replica %d->%d\n", deployment.Name, oldReplicas, replicas)
			err := scaleUp(deployment.Name, lookupNamespace, replicas, deployment, c)
			if err != nil {
				log.Printf("[Scale] %s\n", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else { // scale down
			log.Printf("Scale down %s: replica %d->%d\n", deployment.Name, *deployment.Spec.Replicas, replicas)
			err := scaleDown(deployment.Name, lookupNamespace, replicas, deployment, c)
			if err != nil {
				log.Printf("[Scale] %s\n", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		w.WriteHeader(http.StatusAccepted)
	}
}

// TODO: first use the naive solution, sequentially scale up
func scaleUp(functionName string, functionNamespace string, desiredReplicas int32, deployment *v1.Deployment, c catalog.Catalog) error {
	scaleUpCnt := desiredReplicas - int32(c.FunctionCatalog[functionName].Replicas)
	// log.Printf("scale up count: %d\n", scaleUpCnt)
	fn := c.FunctionCatalog[functionName]
	faasDeployment := types.FunctionDeployment{
		Service:                fn.Name,
		Image:                  fn.Image,
		Namespace:              fn.Namespace,
		EnvProcess:             fn.EnvProcess,
		EnvVars:                fn.EnvVars,
		Constraints:            fn.Constraints,
		Secrets:                fn.Secrets,
		Labels:                 fn.Labels,
		Annotations:            fn.Annotations,
		Limits:                 fn.Limits,
		Requests:               fn.Requests,
		ReadOnlyRootFilesystem: fn.ReadOnlyRootFilesystem,
	}
	// log.Printf("sorted p2 pid: %v\n", c.SortedP2PID)

	// try scale up the function from near to far
	for i := 0; i < len(*c.SortedP2PID) && scaleUpCnt > 0; i++ {
		p2pID := (*c.SortedP2PID)[i]
		// first try to scale up at local cluster
		// deploy first if the function is not exist
		availableFunctionsReplicas := int32(c.NodeCatalog[p2pID].AvailableFunctionsReplicas[functionName])
		log.Printf("p2pID: %s, available replicas: %d\n", p2pID, availableFunctionsReplicas)
		if availableFunctionsReplicas == 0 {
			functionList := k8s.NewFunctionList(functionNamespace, c.NodeCatalog[p2pID].DeployLister)
			// TODO: deploy directly on others k8s, need somewhat publish this infomation
			err, _ := makeFunction(functionNamespace, c.NodeCatalog[p2pID].Factory, functionList, faasDeployment)
			if err != nil {
				log.Printf("make new function error: %v\n", err)
				return err
			}
			// deploy success mean scale up one instance
			scaleUpCnt--
		}
		if scaleUpCnt > 0 {
			clientset := c.NodeCatalog[p2pID].Clientset
			nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
			if err != nil {
				log.Printf("unable to get the number of node: %s, %s", functionName, err)
				return err
			}

			scaleUpCap := int32(len(nodes.Items)) - availableFunctionsReplicas
			log.Printf("Scale up capacity: %d", scaleUpCap)
			if scaleUpCnt < scaleUpCap {
				scaleUpCap = scaleUpCnt
			}
			newReplicas := scaleUpCap + availableFunctionsReplicas
			log.Printf("Scale up to newReplicas: %d", newReplicas)
			deployment.Spec.Replicas = &newReplicas
			if _, err = clientset.AppsV1().Deployments(functionNamespace).Update(context.TODO(), deployment, metav1.UpdateOptions{}); err != nil {
				log.Printf("unable to update function deployment: %s, %s", functionName, err)
				return err
			}
			scaleUpCnt -= scaleUpCap
		}
	}

	return nil
}

func scaleDown(functionName string, functionNamespace string, desiredReplicas int32, deployment *v1.Deployment, c catalog.Catalog) error {
	scaleDownCnt := int32(c.FunctionCatalog[functionName].Replicas) - desiredReplicas

	// remove the function from the far instance
	for i := len(*c.SortedP2PID) - 1; i >= 0 && scaleDownCnt > 0; i++ {
		p2pID := (*c.SortedP2PID)[i]
		availableFunctionsReplicas := int32(c.NodeCatalog[p2pID].AvailableFunctionsReplicas[functionName])
		if availableFunctionsReplicas > 0 {
			// the replica is more than the scale down count
			clientset := c.NodeCatalog[p2pID].Clientset
			if availableFunctionsReplicas > scaleDownCnt {
				replicas := availableFunctionsReplicas - scaleDownCnt
				deployment.Spec.Replicas = &replicas
				if _, err := clientset.AppsV1().Deployments(functionNamespace).Update(context.TODO(), deployment, metav1.UpdateOptions{}); err != nil {
					log.Printf("unable to update function deployment: %s, %s", functionName, err)
					return err
				}
				scaleDownCnt = 0
			} else {
				request := types.DeleteFunctionRequest{
					FunctionName: functionName,
					Namespace:    functionNamespace,
				}
				err := deleteFunction(functionName, clientset, request)
				if err != nil {
					return err
				}
				// c.DeleteAvailableFunctions(functionName)
				scaleDownCnt -= availableFunctionsReplicas
			}
		}
	}

	return nil
}
