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
			// If show zero here, the gateway would consider it as not ready
			tmpFn.AvailableReplicas = 0
		}
		return tmpFn, nil
	}

	return types.FunctionStatus{}, fmt.Errorf("function %s not found ", functionName)
}

func (c Catalog) ListAvailableFunctions(infoLevel InfoLevel) []types.FunctionStatus {
	var functionStatus []types.FunctionStatus
	// functionNameSet := make(map[string]struct{})
	switch infoLevel {
	case LocalLevel:
		// for _, fn := range c.N[selfCatagoryKey].AvailableFunctions {
		// 	if _, exist := functionNameSet[fn.Name]; !exist {
		// 		functionStatus = append(functionStatus, fn)
		// 		functionNameSet[fn.Name] = struct{}{}
		// 	}
		// }
		for fname, replica := range c.NodeCatalog[selfCatagoryKey].AvailableFunctionsReplicas {
			tmpFn := *c.FunctionCatalog[fname]
			tmpFn.AvailableReplicas = replica
			functionStatus = append(functionStatus, tmpFn)
		}
	case ClusterLevel:
		// for _, node := range c {
		// 	// if id == selfCatagoryKey {
		// 	// 	continue
		// 	// }
		// 	for _, fn := range node.AvailableFunctions {
		// 		if _, exist := functionNameSet[fn.Name]; !exist {
		// 			functionStatus = append(functionStatus, fn)
		// 			functionNameSet[fn.Name] = struct{}{}
		// 		}
		// 	}
		// }
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

func (c Catalog) DeleteAvailableFunctions(functionName string) {
	// update the catalog of itself
	c.NodeCatalog[selfCatagoryKey].deleteAvailableFunctions(functionName)

	// update the overall replicat count
	c.updatetReplicasWithFunctionName(functionName)

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
		fmt.Printf("Update function: %s to replicas %d\n", functionName, replicas)
		// remove the function if replicas is zero
		if replicas == 0 {
			delete(c.FunctionCatalog, functionName)
		} else {
			c.FunctionCatalog[functionName].Replicas = replicas
		}
	}
}

// available replicas means the replicas on current node, replicas means the replicas
// in the entire p2p network
func (c Catalog) updatetReplicasWithFunctionName(functionName string) {
	// c.FunctionCatalog[functionName].Replicas

	var replicas uint64 = 0
	for _, node := range c.NodeCatalog {
		if cnt, exist := node.AvailableFunctionsReplicas[functionName]; exist {
			replicas += cnt
		}
	}
	fmt.Printf("Update function: %s to replicas %d\n", functionName, replicas)
	// remove the function if replicas is zero
	if replicas == 0 {
		delete(c.FunctionCatalog, functionName)
	} else {
		c.FunctionCatalog[functionName].Replicas = replicas
	}

}

// update the total replicas with the given nodeinfo
// func (c Catalog) updatetReplicasWithNodeInfo(info NodeInfo) {
// 	for _, fn := range info.AvailableFunctions {
// 		c.updatetReplicasWithFunctionName(fn.Name)
// 	}

// }

// available replicas means the replicas on current node, replicas means the replicas
// in the entire p2p network
// func (c Catalog) UpdatetReplicas(functionName string) {

// }

// func (c Catalog) publishInfo() {

// 	node.infoChan <- &node.NodeInfo
// }

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
	// TODO: receive the initialize available function
	infoMsg := new(NodeInfoMsg)
	err := json.Unmarshal(buf.Bytes(), infoMsg)
	if err != nil {
		log.Printf("deserialized info message error: %s\n", err)
		return
	}
	fmt.Printf("Receive info from publisher %s stream: %v\n", stream.Conn().RemotePeer(), infoMsg)

	// update the info in the node
	unpackNodeInfoMsg(c, infoMsg, peerID)
	// c[stream.Conn().RemotePeer().String()].NodeInfo = *info

	// example
	// buf := make([]byte, 256)
	// n, err := stream.Read(buf)
	// if err != nil && err != io.EOF {
	// 	log.Fatalf("Failed to read from stream: %v", err)
	// }

	// message := string(buf[:n])
	// fmt.Printf("Received direct message: %s\n", message)
}
