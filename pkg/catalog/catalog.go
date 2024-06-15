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
	NodeCatalog         map[string]*Node
	FunctionCatalog     map[string]*types.FunctionStatus
	SortedP2PID         []string
	NewKubeClientWithIp NewKubeClientWithIpFunc
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
	KubeClient
	infoChan chan *NodeInfo
}

// type FunctionReplicas struct {
// 	functionStatus    map[string]types.FunctionStatus
// 	availableReplicas []map[string]uint64
// }

func GetSelfCatalogKey() string {
	return selfCatagoryKey
}

// func (c Catalog) InitAvailableFunctions(fns []types.FunctionStatus) {
// 	c[selfCatagoryKey].AvailableFunctions = fns

// publishInfo(c[selfCatagoryKey].infoChan, &c[selfCatagoryKey].NodeInfo)
// }

func NewCatalog(newKubeClientWithIp NewKubeClientWithIpFunc, totalAmountKubeClient int) Catalog {
	return Catalog{
		NodeCatalog:         make(map[string]*Node),
		FunctionCatalog:     make(map[string]*types.FunctionStatus),
		SortedP2PID:         make([]string, 0, totalAmountKubeClient),
		NewKubeClientWithIp: newKubeClientWithIp,
	}
}

func (c Catalog) NewNodeWithIp(ip string, p2pid string) Node {
	return Node{
		NodeInfo: NodeInfo{
			AvailableFunctionsReplicas: make(map[string]uint64),
			FunctionExecutionTime:      make(map[string]*atomic.Int64),
		},
		NodeMetadata: NodeMetadata{Ip: ip},
		KubeClient:   c.NewKubeClientWithIp(ip, p2pid),
		infoChan:     nil,
	}
}

// add new node into Catalog.NodeCatalog for peerID, ignore if already exist
func (c Catalog) NewNodeCatalogEntry(peerID string, ip string) {
	if _, exist := c.NodeCatalog[peerID]; !exist {
		node := c.NewNodeWithIp(ip, peerID)
		c.NodeCatalog[peerID] = &node
		// c.RankNodeByRTT()
	}
}
