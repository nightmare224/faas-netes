// Copyright 2020 OpenFaaS Author(s)
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/openfaas/faas-provider/types"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	v1corelisters "k8s.io/client-go/listers/core/v1"
)

const (
	//OrchestrationIdentifier identifier string for provider orchestration
	OrchestrationIdentifier = "kubernetes"
	//ProviderName name of the provider
	ProviderName = "faas-netes-ce"
)

const (
	// CPU average overload threshold within one minitues
	CPUOverloadThreshold = 0.90
	// Memory average overload threshold within one minitues
	MemOverloadThreshold = 0.90
)

type CustomInfo struct {
	types.ProviderInfo
	Overload bool `json:"overload"`
}

// MakeInfoHandler creates handler for /system/info endpoint
func MakeInfoHandler(version, sha string, serviceLister v1corelisters.ServiceLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			defer r.Body.Close()
		}

		infoResponse := types.ProviderInfo{
			Orchestration: OrchestrationIdentifier,
			Name:          ProviderName,
			Version: &types.VersionInfo{
				Release: version,
				SHA:     sha,
			},
		}

		var jsonOut []byte
		overload, err := measurePressure(serviceLister)
		if err == nil {
			customResponse := CustomInfo{
				ProviderInfo: infoResponse,
				Overload:     overload,
			}
			jsonOut, err = json.Marshal(customResponse)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		} else {
			jsonOut, err = json.Marshal(infoResponse)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(jsonOut)
	}
}

// add the overloaded infomation in it
func measurePressure(serviceLister v1corelisters.ServiceLister) (bool, error) {
	if serviceLister == nil {
		err := fmt.Errorf("service lister is required for finding prometheus")
		return false, err
	}
	address, err := getPrometheusServiceAddress(serviceLister)
	fmt.Println("Get Address ", address)
	if err != nil {
		err := fmt.Errorf("error getting prometheus service address: %v", err)
		return false, err
	}
	client, err := api.NewClient(api.Config{
		Address: address,
	})
	if err != nil {
		err := fmt.Errorf("error creating client: %v", err)
		return false, err
	}
	v1api := v1.NewAPI(client)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// start query
	overload := false
	// cpu
	cpuQuery := "1 - (rate(node_cpu_seconds_total{mode=\"idle\"}[1m]))"
	CPULoad, err := queryResourceAverageLoad(v1api, ctx, cpuQuery)
	if err != nil {
		err := fmt.Errorf("CPU usage unavailable from Prometheus: %v", err)
		return false, err
	}
	overload = overload || (CPULoad > CPUOverloadThreshold)
	// memory
	// memQuery := "1 - avg_over_time(node_memory_MemAvailable_bytes[1m])/node_memory_MemTotal_bytes"
	memQuery := "1 - ((avg_over_time(node_memory_MemFree_bytes[1m]) + avg_over_time(node_memory_Cached_bytes[1m]) + avg_over_time(node_memory_Buffers_bytes[1m])) / node_memory_MemTotal_bytes)"
	MemLoad, err := queryResourceAverageLoad(v1api, ctx, memQuery)
	if err != nil {
		err := fmt.Errorf("memory usage unavailable from Prometheus: %v", err)
		return false, err
	}
	overload = overload || (MemLoad > MemOverloadThreshold)

	return overload, nil
}
func getPrometheusServiceAddress(serviceLister v1corelisters.ServiceLister) (string, error) {
	svc, err := serviceLister.Services("openfaas").Get("prometheus")
	if err != nil {
		// fmt.Println(err)
		return "", err
	}
	ip := svc.Spec.ClusterIP
	port := svc.Spec.Ports[0].Port
	address := fmt.Sprintf("http://%s:%d", ip, port)

	return address, nil
}
func queryResourceAverageLoad(promClient v1.API, ctx context.Context, query string) (model.SampleValue, error) {

	result, _, err := promClient.Query(ctx, query, time.Now(), v1.WithTimeout(5*time.Second))
	if err != nil {
		err := fmt.Errorf("error querying Prometheus: %v", err)
		return 0, err
	}

	switch {
	case result.Type() == model.ValVector:
		var avgLoad model.SampleValue = 0
		vectorVal := result.(model.Vector)
		for _, elem := range vectorVal {
			avgLoad += elem.Value
		}
		return avgLoad / model.SampleValue(len(vectorVal)), nil
	default:
		err := fmt.Errorf("unexpected value type %q", result.Type())
		return 0, err
	}
}
