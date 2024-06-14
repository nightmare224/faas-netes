package catalog

import (
	"context"
	"fmt"
	"log"
	"os"

	libp2p "github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/openfaas/faas-provider/types"
)

// const pubKeySelf = "/tmp/faasd-p2p/pubKey"
const privKeySelf = "/opt/p2p/privKey"
const faasProtocolID = protocol.ID("/faas-initialize/1.0.0")
const faasP2PIp = "0.0.0.0"
const faasP2PPort = "30828"

func InitInfoNetwork(c Catalog) error {
	ctx := context.Background()
	host := newLibp2pHost()
	ps := newPubSubRouter(ctx, host)

	// create the stream for initialization
	host.SetStreamHandler(faasProtocolID, func(stream network.Stream) {
		c.streamAvailableFunctions(stream)
	})

	// create a info room as itself ID
	ir, err := subscribeInfoRoom(ctx, ps, host.ID().String(), host.ID(), c)
	if err != nil {
		return err
	}

	// discovery the other host and join their room
	discoveryErr := setupDiscovery(host, ps, c)
	if discoveryErr != nil {
		return discoveryErr
	}

	// the info of itself
	if _, exist := c.NodeCatalog[selfCatagoryKey]; exist {
		c.NodeCatalog[selfCatagoryKey].NodeMetadata = NodeMetadata{
			Ip: extractIP4fromMultiaddr(host.Addrs()[0]),
		}
		c.NodeCatalog[selfCatagoryKey].infoChan = ir.infoChan
	} else {
		return fmt.Errorf("the self catalog should be initialize beforehand")
	}

	return nil
}

func GetSelfFaasP2PIp() string {
	return faasP2PIp
}

func newLibp2pHost() host.Host {

	privKeyData, err := os.ReadFile(privKeySelf)
	if err != nil {
		log.Printf("Failed to read private key file: %s", err)
		panic(err)
	}
	privKey, err := crypto.UnmarshalPrivateKey(privKeyData)
	if err != nil {
		log.Printf("Failed to extract key from message key data: %s", err)
		panic(err)
	}
	multiaddr := getMultiaddr()
	host, err := libp2p.New(
		libp2p.ListenAddrStrings(multiaddr),
		libp2p.Identity(privKey),
	)
	if err != nil {
		log.Printf("Failed to create new host: %s", err)
		panic(err)
	}
	log.Printf("Created new p2p host %s (%s)\n", host.ID().String(), multiaddr)

	return host
}
func getMultiaddr() string {
	// ip, port := os.Getenv("FAAS_P2P_IP"), os.Getenv("FAAS_P2P_PORT")
	ip := types.ParseString(os.Getenv("FAAS_P2P_IP"), faasP2PIp)
	port := types.ParseString(os.Getenv("FAAS_P2P_PORT"), faasP2PPort)
	multiaddr := fmt.Sprintf("/ip4/%s/udp/%s/quic-v1", ip, port)

	return multiaddr
}

// create a new PubSub service using the GossipSub router
func newPubSubRouter(ctx context.Context, host host.Host) *pubsub.PubSub {
	ps, err := pubsub.NewGossipSub(ctx, host)
	if err != nil {
		panic(err)
	}
	return ps
}
