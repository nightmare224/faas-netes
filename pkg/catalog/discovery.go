package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/openfaas/faas-provider/types"
)

const DiscoveryServiceTag = "faasd-localcluster"
const pubKeyPeerPath = "/opt/p2p/pubKey-peer/"
const mode = "static" //or mdns

// discoveryNotifee gets notified when we find a new peer via mDNS discovery
type faasNotifiee struct {
	h  host.Host
	ps *pubsub.PubSub
	c  Catalog
}

// type faasNotifiee struct {
// 	h host.Host
// 	c Catalog
// }

func setupDiscovery(h host.Host, ps *pubsub.PubSub, c Catalog) error {
	// setup mDNS discovery to find local peers
	switch mode {
	case "static":
		notifee := &faasNotifiee{h: h, ps: ps, c: c}
		h.Network().Notify(notifee)
		//TODO: maybe should be go rooutine, just like mdns
		return staticDiscovery(notifee)
	case "mdns":
		s := mdns.NewMdnsService(h, DiscoveryServiceTag, &faasNotifiee{h: h, ps: ps, c: c})
		return s.Start()
	default:
		return fmt.Errorf("discover peer mode %s not found", mode)
	}
}
func staticDiscovery(n *faasNotifiee) error {
	port := types.ParseString(os.Getenv("FAAS_P2P_PORT"), faasP2PPort)
	err := filepath.WalkDir(pubKeyPeerPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, _ := os.Lstat(path)
		if (fi.Mode()&fs.ModeSymlink == 0) && !d.IsDir() {
			peerIP := d.Name()
			peerID := readIDFromPubKey(filepath.Join(pubKeyPeerPath, peerIP))
			// skip when found itself
			if peerID == n.h.ID() {
				return nil
			}
			maddr, err := ma.NewMultiaddr(fmt.Sprintf("/ip4/%s/udp/%s/quic-v1", peerIP, port))
			if err != nil {
				log.Println(err)
				return err
			}
			pi := peer.AddrInfo{
				ID:    peerID,
				Addrs: []ma.Multiaddr{maddr},
			}
			// this is when found peer subjectly, not objectly
			// TODO: if it already have instance than don't need to do this subjectly
			if _, exist := n.c.NodeCatalog[peerID.String()]; !exist {
				log.Printf("Do the handle peer found from pi %s\n", peerID)
				go n.HandlePeerFound(pi)
			}
		}

		return nil
	})
	if err != nil {
		log.Fatalf("Error load p2p peer public key: %s", err.Error())
	}
	return nil
}
func extractIP4fromMultiaddr(maddr ma.Multiaddr) string {
	val, err := maddr.ValueForProtocol(ma.P_IP4)
	if err != nil {
		log.Printf("Cannot extract IP address: %s", err)
		return ""
	}

	return val
}

func (n *faasNotifiee) HandlePeerFound(pi peer.AddrInfo) {
	log.Printf("Discovered new peer %s\n", pi.ID)

	// create the instance in catalog and then connect,
	// to prevent the connect function call this function again
	// init the catagory for the find peer
	infoRoomName := pi.ID.String()
	node := NewNode()
	node.Ip = extractIP4fromMultiaddr(pi.Addrs[0])
	n.c.NodeCatalog[infoRoomName] = &node

	// make sure the connection with peer, if already connected would not connect aggain
	ctx := context.Background()
	err := n.h.Connect(ctx, pi)
	if err != nil {
		log.Printf("error connecting to peer %s, ignore this peer", err)
		return
	}

	// subscribe to the room of remote peer
	// TODO: Subscribe to the publish room  after reconnect
	_, subErr := subscribeInfoRoom(ctx, n.ps, infoRoomName, n.h.ID(), n.c)
	if subErr != nil {
		err := fmt.Errorf("error subcribe to info room: %s", subErr)
		log.Fatal(err)
	}

}

// func InitAvailableFunctions(host host.Host, peerID peer.ID) {
// 	host.NewStream()
// }

func readIDFromPubKey(filepath string) peer.ID {
	pubKeyData, err := os.ReadFile(filepath)
	if err != nil {
		log.Printf("Failed to read public key file: %s", err)
		panic(err)
	}
	key, err := crypto.UnmarshalPublicKey(pubKeyData)
	if err != nil {
		log.Printf("Failed to extract key from message key data: %s", err)
		panic(err)
	}
	idFromKey, err := peer.IDFromPublicKey(key)
	if err != nil {
		log.Printf("Failed to extract ID from private key: %s", err)
		panic(err)
	}

	return idFromKey
}

func (n *faasNotifiee) Listen(network network.Network, maddr ma.Multiaddr) {

}

func (n *faasNotifiee) ListenClose(network network.Network, maddr ma.Multiaddr) {

}

// send the initial available function if the new peer join
func (n *faasNotifiee) Connected(network network.Network, conn network.Conn) {
	// fmt.Println("Peer store:", n.h.Peerstore().Peers())

	remotePeer := conn.RemotePeer()
	log.Printf("Peer Connected: %s\n", remotePeer)
	// if the is new peer than do the handler peer found first
	if _, exist := n.c.NodeCatalog[remotePeer.String()]; !exist {
		pi := peer.AddrInfo{
			ID:    remotePeer,
			Addrs: []ma.Multiaddr{conn.RemoteMultiaddr()},
		}
		n.HandlePeerFound(pi)
	}
	// if do not do it concurrently, the peers will block to try new stream at the same time
	go func() {
		stream, err := n.h.NewStream(context.Background(), remotePeer, faasProtocolID)
		if err != nil {
			log.Fatalf("Failed to open stream: %v", err)
			return
		}
		defer stream.Close()
		infoMsg := packNodeInfoMsg(n.c, &n.c.NodeCatalog[selfCatagoryKey].NodeInfo)
		infoBytes, err := json.Marshal(infoMsg)
		if err != nil {
			log.Printf("serialized info message error: %s\n", err)
			return
		}
		_, err = stream.Write([]byte(infoBytes))
		if err != nil {
			log.Fatalf("Failed to send message: %v", err)
			return
		}
		log.Printf("Message stream to peer %s", remotePeer)
	}()

}

func (n *faasNotifiee) Disconnected(network network.Network, conn network.Conn) {
	log.Printf("Encounter disconnected from %s", conn.RemotePeer())
	// TODO: maybe clean the available replica during disconnect, or just delete entire node?
}
