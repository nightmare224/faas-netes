package catalog

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/openfaas/faas-provider/types"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"

	// v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

func (node *Node) addAvailableFunctions(functionStatus types.FunctionStatus) {
	node.FunctionExecutionTime[functionStatus.Name] = new(atomic.Int64)
	node.FunctionExecutionTime[functionStatus.Name].Store(1)
	node.AvailableFunctionsReplicas[functionStatus.Name] = functionStatus.AvailableReplicas
}

func (node *Node) updateAvailableFunctions(functionStatus types.FunctionStatus) {
	node.AvailableFunctionsReplicas[functionStatus.Name] = functionStatus.AvailableReplicas
}

func (node *Node) deleteAvailableFunctions(functionName string) {
	delete(node.AvailableFunctionsReplicas, functionName)
	// delete(node.FunctionExecutionTime, functionName)
}

func (node *Node) ListenUpdateInfo(clientProm *promv1.API) {
	for {
		// current disable the local replica health monitor
		if /* node.updateAvailableReplicas(clientContainerd) || */ node.updatePressure(clientProm) {
			node.publishInfo()
		}
		time.Sleep(infoUpdateIntervalSec * time.Second)
	}

}

// add the overloaded infomation in it
func (node *Node) updatePressure(client *promv1.API) bool {
	updated := false
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// start query
	// cpu
	cpuQuery := "1 - (rate(node_cpu_seconds_total{mode=\"idle\"}[30s]))"
	CPULoad, err := queryResourceAverageLoad(client, ctx, cpuQuery)
	if err != nil {
		log.Fatalf("CPU usage unavailable from Prometheus: %v", err)
		return updated
	}
	overload_update := (CPULoad > CPUOverloadThreshold)
	// memory
	// memQuery := "1 - avg_over_time(node_memory_MemAvailable_bytes[30s])/node_memory_MemTotal_bytes"
	memQuery := "1 - ((avg_over_time(node_memory_MemFree_bytes[30s]) + avg_over_time(node_memory_Cached_bytes[30s]) + avg_over_time(node_memory_Buffers_bytes[30s])) / node_memory_MemTotal_bytes)"
	MemLoad, err := queryResourceAverageLoad(client, ctx, memQuery)
	if err != nil {
		log.Fatalf("memory usage unavailable from Prometheus: %v", err)
		return updated
	}
	overload_update = overload_update || (MemLoad > MemOverloadThreshold)
	// time.Sleep(time.Second * 10)
	// fmt.Println("The update overload: ", overload_update)
	// update
	if overload_update != node.Overload {
		node.Overload = overload_update
		updated = true
	}
	return updated
}

// func getPrometh
func queryResourceAverageLoad(promClient *promv1.API, ctx context.Context, query string) (model.SampleValue, error) {

	result, _, err := (*promClient).Query(ctx, query, time.Now(), promv1.WithTimeout(5*time.Second))
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

func (node *Node) publishInfo() {

	node.infoChan <- &node.NodeInfo
}
