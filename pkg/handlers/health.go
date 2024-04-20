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
	"time"

	"github.com/openfaas/faas-provider/proxy"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

const (
	// CPU average overload threshold within one minitues
	CPUOverloadThreshold = 0.50
	// Memory average overload threshold within one minitues
	MemOverloadThreshold = 0.50
)

type CustomHealth struct {
	Overload bool `json:"overload"`
}

// MakeHealthHandler returns 200/OK when healthy
func MakeHealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		overload_showed := r.URL.Query().Get("overload")
		// If there are no values associated with the key, Get returns the empty string
		if overload_showed == "0" || overload_showed == "" {
			w.WriteHeader(http.StatusOK)
			return
		}
		overload, err := MeasurePressure()
		if err != nil {
			fmt.Printf("Unable to get metric from pormetheus: %s", err)
			w.WriteHeader(http.StatusOK)
			return
		}

		jsonOut, err := json.Marshal(CustomHealth{overload})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(jsonOut)
	}
}

func GetExertnalPressure(resolver proxy.BaseURLResolver) (bool, error) {

	hostUrl, err := resolver.Resolve("")
	if err != nil {
		err := fmt.Errorf("unable to resolve remote host: %v", err)
		return false, err
	}
	healthzUrl := fmt.Sprintf("%s/healthz?overload=1", hostUrl.String())
	resp, err := http.Get(healthzUrl)
	if err != nil {
		err := fmt.Errorf("unable to get health: %v", err)
		return false, err
	}
	defer resp.Body.Close()

	// defer upstreamCall.Body.Close()

	var health CustomHealth

	body, _ := io.ReadAll(resp.Body)
	err = json.Unmarshal(body, &health)
	if err != nil {
		log.Printf("Error unmarshalling provider json from body %s. Error %s\n", body, err.Error())
	}

	return health.Overload, nil

}

// add the overloaded infomation in it
func MeasurePressure() (bool, error) {
	// if serviceLister == nil {
	// 	err := fmt.Errorf("service lister is required for finding prometheus")
	// 	return false, err
	// }
	// address, err := getPrometheusServiceAddress(serviceLister)
	// fmt.Println("Get Address ", address)
	// if err != nil {
	// 	err := fmt.Errorf("error getting prometheus service address: %v", err)
	// 	return false, err
	// }
	address := "http://prometheus.openfaas.svc:9090"
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

// func getPrometheusServiceAddress(serviceLister v1corelisters.ServiceLister) (string, error) {
// 	svc, err := serviceLister.Services("openfaas").Get("prometheus")
// 	if err != nil {
// 		// fmt.Println(err)
// 		return "", err
// 	}
// 	ip := svc.Spec.ClusterIP
// 	port := svc.Spec.Ports[0].Port
// 	address := fmt.Sprintf("http://%s:%d", ip, port)

//		return address, nil
//	}
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
