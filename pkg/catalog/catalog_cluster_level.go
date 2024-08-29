package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/openfaas/faas-provider/types"
)

func (c Catalog) GetAvailableFunction(functionName string) (types.FunctionStatus, error) {
	if fn, exist := c.FunctionCatalog[functionName]; exist {
		tmpFn := *fn
		if _, exist := c.NodeCatalog[selfCatagoryKey].AvailableFunctionsReplicas[functionName]; exist {
			tmpFn.AvailableReplicas = c.NodeCatalog[selfCatagoryKey].AvailableFunctionsReplicas[functionName]
		} else {
			tmpFn.AvailableReplicas = 0
		}
		return tmpFn, nil
	}

	return types.FunctionStatus{}, fmt.Errorf("function %s not found ", functionName)
}

func (c Catalog) ListAvailableFunctions(infoLevel InfoLevel) []types.FunctionStatus {
	var functionStatus []types.FunctionStatus

	switch infoLevel {
	case LocalLevel:
		for fname, replica := range c.NodeCatalog[selfCatagoryKey].AvailableFunctionsReplicas {
			tmpFn := *c.FunctionCatalog[fname]
			tmpFn.AvailableReplicas = replica
			functionStatus = append(functionStatus, tmpFn)
		}
	case ClusterLevel:
		for fname, fn := range c.FunctionCatalog {
			tmpFn := *fn
			if _, exist := c.NodeCatalog[selfCatagoryKey].AvailableFunctionsReplicas[fname]; exist {
				tmpFn.AvailableReplicas = c.NodeCatalog[selfCatagoryKey].AvailableFunctionsReplicas[fname]
			} else {
				tmpFn.AvailableReplicas = 0
			}
			functionStatus = append(functionStatus, tmpFn)
		}
	}
	return functionStatus
}

func (c Catalog) AddAvailableFunctions(functionStatus types.FunctionStatus) {
	// update the catalog of itself
	c.NodeCatalog[selfCatagoryKey].addAvailableFunctions(functionStatus)

	// add to FunctionCatalog if not exist
	if _, exist := c.FunctionCatalog[functionStatus.Name]; !exist {
		c.FunctionCatalog[functionStatus.Name] = &functionStatus
	}

	// update the overall replicat count
	c.updatetReplicasWithFunctionName(functionStatus.Name)

	// publish info
	c.NodeCatalog[selfCatagoryKey].publishInfo()
}

func (c Catalog) UpdateAvailableFunctions(functionStatus types.FunctionStatus) {
	// update the catalog of itself
	c.NodeCatalog[selfCatagoryKey].updateAvailableFunctions(functionStatus)

	// update the overall replicat count
	c.updatetReplicasWithFunctionName(functionStatus.Name)

	// publish info
	c.NodeCatalog[selfCatagoryKey].publishInfo()
}

func (c Catalog) DeleteAvailableFunctions(functionName string) {
	// update the catalog of itself
	c.NodeCatalog[selfCatagoryKey].deleteAvailableFunctions(functionName)

	// update the overall replicat count
	c.updatetReplicasWithFunctionName(functionName)

	// delete the function in function catalog when there is explict delete like this
	if c.FunctionCatalog[functionName].Replicas == 0 {
		delete(c.FunctionCatalog, functionName)
	}

	// publish info
	c.NodeCatalog[selfCatagoryKey].publishInfo()
}

func (c Catalog) updatetReplicas() {
	// c.FunctionCatalog[functionName].Replicas
	for functionName, _ := range c.FunctionCatalog {
		var replicas uint64 = 0
		for _, node := range c.NodeCatalog {
			if cnt, exist := node.AvailableFunctionsReplicas[functionName]; exist {
				replicas += cnt
			}
		}
		c.FunctionCatalog[functionName].Replicas = replicas
	}
}

// available replicas means the replicas on current node, replicas means the replicas
// in the entire p2p network
func (c Catalog) updatetReplicasWithFunctionName(functionName string) {

	var replicas uint64 = 0
	for _, node := range c.NodeCatalog {
		if cnt, exist := node.AvailableFunctionsReplicas[functionName]; exist {
			replicas += cnt
		}
	}
	c.FunctionCatalog[functionName].Replicas = replicas
}

// the handler of receive the initialization function info
func (c Catalog) streamAvailableFunctions(stream network.Stream) {
	defer stream.Close()

	// Add nod to nodecatalog if the node not create yet
	peerID := stream.Conn().RemotePeer().String()
	ip := extractIP4fromMultiaddr(stream.Conn().RemoteMultiaddr())
	c.NewNodeCatalogEntry(peerID, ip)

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, stream); err != nil {
		log.Fatalf("Failed to read from stream: %v", err)
		return
	}

	infoMsg := new(NodeInfoMsg)
	err := json.Unmarshal(buf.Bytes(), infoMsg)
	if err != nil {
		log.Printf("deserialized info message error: %s\n", err)
		return
	}

	// update the info in the node
	unpackNodeInfoMsg(c, infoMsg, peerID)

}
