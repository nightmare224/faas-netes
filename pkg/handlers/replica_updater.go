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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MakeReplicaUpdater updates desired count of replicas
func MakeReplicaUpdater(defaultNamespace string, c catalog.Catalog) http.HandlerFunc {
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

		if req.Replicas <= 0 {
			http.Error(w, "replicas cannot be set to 0 in OpenFaaS CE",
				http.StatusBadRequest)
			return
		}

		// options := metav1.GetOptions{
		// 	TypeMeta: metav1.TypeMeta{
		// 		Kind:       "Deployment",
		// 		APIVersion: "apps/v1",
		// 	},
		// }

		// deployment, err := c.NodeCatalog[catalog.GetSelfCatalogKey()].Clientset.AppsV1().Deployments(lookupNamespace).Get(context.TODO(), functionName, options)

		// if err != nil {
		// 	w.WriteHeader(http.StatusInternalServerError)
		// 	w.Write([]byte("Unable to lookup function deployment " + functionName))
		// 	log.Println(err)
		// 	return
		// }

		fn, fnExist := c.FunctionCatalog[functionName]
		if !fnExist {
			msg := fmt.Sprintf("service %s not found", functionName)
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
		// the isOffloadRequest is for the faasd
		if oldReplicas == replicas {
			log.Printf("Scale %s: stay replica %d\n", functionName, replicas)
			w.WriteHeader(http.StatusNoContent)
			return
		} else if /* !catalog.EnabledOffload  ||*/ isOffloadRequest(r) {
			// no other way to go, just scale up all here, it should be the cloud side if it does not enabled offload
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
			deployment.Spec.Replicas = &replicas
			if _, err = c.NodeCatalog[catalog.GetSelfCatalogKey()].Clientset.AppsV1().Deployments(lookupNamespace).Update(context.TODO(), deployment, metav1.UpdateOptions{}); err != nil {
				w.Write([]byte("Unable to scale deployment " + functionName))
				log.Println(err)
				return
			}
		} else if oldReplicas < replicas { //scale up
			log.Printf("Scale up %s: replica %d->%d\n", functionName, oldReplicas, replicas)
			err := scaleUp(functionName, lookupNamespace, replicas, c)
			if err != nil {
				log.Printf("[Scale] %s\n", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else { // scale down
			log.Printf("Scale down %s: replica %d->%d\n", functionName, oldReplicas, replicas)
			err := scaleDown(functionName, lookupNamespace, replicas, c)
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
func scaleUp(functionName string, functionNamespace string, desiredReplicas int32, c catalog.Catalog) error {
	scaleUpCnt := desiredReplicas - int32(c.FunctionCatalog[functionName].Replicas)

	// if the offload is not enable, means that the localhost is the only choose, so the length is 1
	numNode := 1
	if catalog.EnabledOffload {
		numNode = len(*c.SortedP2PID)
	}
	// try scale up the function from near to far
	for i := 0; i < numNode && scaleUpCnt > 0; i++ {
		p2pID := (*c.SortedP2PID)[i]
		if c.NodeCatalog[p2pID].Overload {
			log.Printf("node %s is overload, skip scale up", p2pID)
			continue
		}
		// first try to scale up at local cluster
		// deploy first if the function is not exist
		availableFunctionsReplicas := int32(c.NodeCatalog[p2pID].AvailableFunctionsReplicas[functionName])
		log.Printf("p2pID: %s, available replicas: %d\n", p2pID, availableFunctionsReplicas)
		if availableFunctionsReplicas == 0 {
			err := deployFunctionByP2PID(functionNamespace, functionName, p2pID, c)
			if err != nil {
				log.Printf("make new function error: %v\n", err)
				return err
			}
			// deploy success mean scale up one instance
			scaleUpCnt--
			availableFunctionsReplicas += 1
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
			options := metav1.GetOptions{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Deployment",
					APIVersion: "apps/v1",
				},
			}
			// different client have differnt deployment uuid
			deployment, err := c.NodeCatalog[p2pID].Clientset.AppsV1().Deployments(functionNamespace).Get(context.TODO(), functionName, options)
			if err != nil {
				log.Println(err)
				return err
			}
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

// TODO: If all the prometheus trigger the scale down at the same time, will it mistakely delete?
func scaleDown(functionName string, functionNamespace string, desiredReplicas int32, c catalog.Catalog) error {
	scaleDownCnt := int32(c.FunctionCatalog[functionName].Replicas) - desiredReplicas

	// if the offload is not enable, means that the localhost is the only choose, so the length is 1
	// ! always set to 1, as we want only scale down in its own unit, as we don't know the gateway status of other node
	numNode := 1
	// if catalog.EnabledOffload {
	// 	numNode = len(*c.SortedP2PID)
	// }

	// try scale down the function from near to far (take care of itself first)
	for i := 0; i < numNode && scaleDownCnt > 0; i++ {
		p2pID := (*c.SortedP2PID)[i]
		availableFunctionsReplicas := int32(c.NodeCatalog[p2pID].AvailableFunctionsReplicas[functionName])
		log.Printf("p2pID: %s, available replicas: %d\n", p2pID, availableFunctionsReplicas)
		if availableFunctionsReplicas > 0 {
			// the replica is more than the scale down count
			clientset := c.NodeCatalog[p2pID].Clientset
			if availableFunctionsReplicas > scaleDownCnt {
				replicas := availableFunctionsReplicas - scaleDownCnt
				options := metav1.GetOptions{
					TypeMeta: metav1.TypeMeta{
						Kind:       "Deployment",
						APIVersion: "apps/v1",
					},
				}
				// different client have differnt deployment uuid
				deployment, err := c.NodeCatalog[p2pID].Clientset.AppsV1().Deployments(functionNamespace).Get(context.TODO(), functionName, options)
				if err != nil {
					log.Println(err)
					return err
				}
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
				err := deleteFunction(functionNamespace, clientset, request)
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

func deployFunctionByP2PID(functionNamespace string, functionName string, targetP2PID string, c catalog.Catalog) error {
	targetFunction, exist := c.FunctionCatalog[functionName]
	if !exist {
		err := fmt.Errorf("no endpoints available for: %s", functionName)
		return err
	}
	deployment := types.FunctionDeployment{
		Service:                targetFunction.Name,
		Image:                  targetFunction.Image,
		Namespace:              targetFunction.Namespace,
		EnvProcess:             targetFunction.EnvProcess,
		EnvVars:                targetFunction.EnvVars,
		Constraints:            targetFunction.Constraints,
		Secrets:                targetFunction.Secrets,
		Labels:                 targetFunction.Labels,
		Annotations:            targetFunction.Annotations,
		Limits:                 targetFunction.Limits,
		Requests:               targetFunction.Requests,
		ReadOnlyRootFilesystem: targetFunction.ReadOnlyRootFilesystem,
	}
	functionList := k8s.NewFunctionList(functionNamespace, c.NodeCatalog[targetP2PID].DeployLister)
	err, _ := makeFunction(functionNamespace, c.NodeCatalog[targetP2PID].Factory, functionList, deployment)
	if err != nil {
		return err
	}
	return nil
}
