package catalog

// import (
// 	"encoding/json"
// 	"fmt"
// 	"log"
// 	"net"
// 	"net/http"
// 	"net/url"
// 	"os"
// 	"path"
// 	"slices"
// 	"strings"
// 	"time"

// 	"github.com/openfaas/go-sdk"
// 	probing "github.com/prometheus-community/pro-bing"
// )

// type FaasP2PMapping struct {
// 	FaasClient *sdk.Client
// 	P2PID      string
// }

// // the list of FaasP2PMapping is sorted by the network distance
// type FaasP2PMappingList []FaasP2PMapping

// // type FaasClient sdk.Client

// const faasConfigPath = "/opt/faasd/secrets/faasclient/"

// type FaasConfig struct {
// 	Ip       string `json:"ip"`
// 	Port     string `json:"port"`
// 	Path     string `json:"path"`
// 	User     string `json:"user"`
// 	Password string `json:"password"`
// }

// func (mapping *FaasP2PMapping) Resolve(name string) (url.URL, error) {
// 	// mapping.FaasClient.GatewayURL
// 	// functionAddr := url.URL{
// 	// 	Scheme: "http",
// 	// 	Host:   fmt.Sprintf("%s:%s", config.Ip, config.Port),
// 	// }

// 	return *mapping.FaasClient.GatewayURL, nil
// }

// func (l FaasP2PMappingList) GetByP2PID(p2pID string) FaasP2PMapping {
// 	for _, mapping := range l {
// 		if mapping.P2PID == p2pID {
// 			return mapping
// 		}
// 	}

// 	return FaasP2PMapping{}
// }

// func NewFaasP2PMappingList(c Catalog) FaasP2PMappingList {

// 	faasP2PMappingList := FaasP2PMappingList{
// 		// also add itself into it (for itself, it don't need the faas client)
// 		{
// 			FaasClient: nil,
// 			P2PID:      selfCatagoryKey,
// 		},
// 	}
// 	//
// 	faasClients := newFaasClients(c.NodeCatalog[selfCatagoryKey].Ip)
// 	for _, client := range faasClients {
// 		for p2pID, p2pNode := range c.NodeCatalog {
// 			if strings.HasPrefix(client.GatewayURL.Host, p2pNode.Ip) {
// 				mapping := FaasP2PMapping{
// 					FaasClient: client,
// 					P2PID:      p2pID,
// 				}
// 				faasP2PMappingList = append(faasP2PMappingList, mapping)
// 				break
// 			}
// 		}

// 		// testFaasClient(client)
// 	}

// 	rankClientsByRTT(faasP2PMappingList)

// 	// log.Println(faasP2PMappingList)
// 	return faasP2PMappingList
// }

// // put it from the fastest client to slowest client
// // func rankClientsByRTT(clients []*sdk.Client) {

// // 	// make this run every
// // 	var sortedRTTClients []*sdk.Client
// // 	var RTTs []time.Duration
// // 	RTTtoIdx := make(map[time.Duration]int)
// // 	for idx, client := range clients {
// // 		startTime := time.Now()
// // 		conn, err := net.DialTimeout("tcp", client.GatewayURL.Host, 5*time.Second)
// // 		if err != nil {
// // 			fmt.Printf("Measure RTT TCP connection error: %s", err.Error())
// // 		}
// // 		rtt := time.Since(startTime)
// // 		conn.Close()
// // 		RTTtoIdx[rtt] = idx
// // 		RTTs = append(RTTs, rtt)
// // 		fmt.Println("RTT: ", rtt, "URL: ", client.GatewayURL.Host)
// // 	}
// // 	slices.Sort(RTTs)
// // 	for _, rtt := range RTTs {
// // 		sortedRTTClients = append(sortedRTTClients, clients[RTTtoIdx[rtt]])
// // 	}
// // 	// copy back to original array
// // 	for i, client := range sortedRTTClients {
// // 		clients[i] = client
// // 	}

// // }

// // put it from the fastest client to slowest client
// func rankClientsByRTT(faasP2PMappingList FaasP2PMappingList) {

// 	// TODO: make this run periodically?
// 	var RTTs []time.Duration
// 	RTTtoMapping := make(map[time.Duration]FaasP2PMapping)
// 	for _, mapping := range faasP2PMappingList {
// 		// for itself it is 0
// 		rtt := time.Duration(0)
// 		if mapping.P2PID != selfCatagoryKey {
// 			// startTime := time.Now()
// 			// can not ping to the faas gateway as this point it haven't start up yet
// 			// conn, err := net.DialTimeout("tcp", mapping.FaasClient.GatewayURL.Host, 5*time.Second)
// 			// if err != nil {
// 			// 	fmt.Printf("Measure RTT TCP connection error: %s", err.Error())
// 			// 	panic(err)
// 			// }
// 			// rtt = time.Since(startTime)
// 			// conn.Close()
// 			ip, _, _ := net.SplitHostPort(mapping.FaasClient.GatewayURL.Host)
// 			pinger, err := probing.NewPinger(ip)
// 			if err != nil {
// 				panic(err)
// 			}
// 			pinger.Count = 3
// 			err = pinger.Run() // Blocks until finished.
// 			if err != nil {
// 				panic(err)
// 			}
// 			stats := pinger.Statistics()
// 			rtt = stats.AvgRtt
// 		}
// 		RTTtoMapping[rtt] = mapping
// 		RTTs = append(RTTs, rtt)
// 		fmt.Println("RTT: ", rtt, "P2P ID: ", mapping.P2PID)
// 	}
// 	slices.Sort(RTTs)
// 	// copy back to original array
// 	for i, rtt := range RTTs {
// 		faasP2PMappingList[i] = RTTtoMapping[rtt]
// 	}

// }

// // func testFaasClient(client *sdk.Client) {
// // 	fns, err := client.GetFunctions(context.Background(), "openfaas-fn")
// // 	if err != nil {
// // 		log.Printf("test error: %s", err)
// // 		return
// // 	}
// // 	log.Println(fns)
// // }

// func newFaasClients(selfIP string) []*sdk.Client {
// 	var faasClients []*sdk.Client
// 	dir, _ := os.ReadDir(pubKeyPeerPath)
// 	for _, entry := range dir {
// 		filename := entry.Name()
// 		// skip itself config
// 		if filename == selfIP {
// 			continue
// 		}
// 		faasConfigData, err := os.ReadFile(path.Join(faasConfigPath, filename))
// 		if err != nil {
// 			log.Printf("Error reading JSON file: %s\n", err)
// 			continue
// 		}

// 		var faasConfig FaasConfig
// 		err = json.Unmarshal(faasConfigData, &faasConfig)
// 		if err != nil {
// 			log.Printf("Failed to extract kubeconfig from json file: %s", err)
// 			continue
// 		}
// 		// fmt.Println("faasConfig:", faasConfig)
// 		hosturl := fmt.Sprintf("http://%s:%s", faasConfig.Ip, faasConfig.Port)

// 		gatewayURL, _ := url.Parse(hosturl)
// 		auth := &sdk.BasicAuth{
// 			Username: faasConfig.User,
// 			Password: faasConfig.Password,
// 		}
// 		client := sdk.NewClient(gatewayURL, auth, http.DefaultClient)
// 		faasClients = append(faasClients, client)
// 	}
// 	// jsonData, err := os.ReadFile("data.json") // Go 1.16+ simplifies file reading

// 	return faasClients
// }
