package catalog

import (
	"sync/atomic"

	"github.com/openfaas/faas-provider/types"
)

// own itself use this key, other will use the p2p id as key
var selfCatagoryKey string = "0"

type InfoLevel int

const (
	LocalLevel InfoLevel = iota
	ClusterLevel
)

const infoUpdateIntervalSec = 10

const (
	// CPU average overload threshold within one minitues
	CPUOverloadThreshold = 0.80
	// Memory average overload threshold within one minitues
	MemOverloadThreshold = 0.80
)

// key is the peer ID in string
// type NodeCatalog map[string]*Node
// type FunctionCatalog map[string]types.FunctionStatus

type Catalog struct {
	// to prevent reinsert for modify Node by using pointer
	NodeCatalog     map[string]*Node
	FunctionCatalog map[string]*types.FunctionStatus
}

type NodeInfo struct {
	// FunctionExecutionTime      map[string]time.Duration
	FunctionExecutionTime      map[string]*atomic.Int64
	AvailableFunctionsReplicas map[string]uint64
	Overload                   bool
}

type NodeInfoMsg struct {
	AvailableFunctions []types.FunctionStatus `json:"availableFunctions"`
	Overload           bool                   `json:"overload"`
}

type NodeMetadata struct {
	Ip       string `json:"ip"`
	Hostname string `json:"hostname"`
}

type Node struct {
	NodeInfo
	NodeMetadata
	infoChan chan *NodeInfo
}

// type FunctionReplicas struct {
// 	functionStatus    map[string]types.FunctionStatus
// 	availableReplicas []map[string]uint64
// }

func (c Catalog) GetSelfCatalogKey() string {
	return selfCatagoryKey
}

// func (c Catalog) InitAvailableFunctions(fns []types.FunctionStatus) {
// 	c[selfCatagoryKey].AvailableFunctions = fns

// publishInfo(c[selfCatagoryKey].infoChan, &c[selfCatagoryKey].NodeInfo)
// }

func NewCatalog() Catalog {
	return Catalog{
		NodeCatalog:     make(map[string]*Node),
		FunctionCatalog: make(map[string]*types.FunctionStatus),
	}
}

func NewNode() Node {
	return Node{
		NodeInfo: NodeInfo{
			AvailableFunctionsReplicas: make(map[string]uint64),
			FunctionExecutionTime:      make(map[string]*atomic.Int64),
		},
		NodeMetadata: NodeMetadata{},
		infoChan:     nil,
	}
}
