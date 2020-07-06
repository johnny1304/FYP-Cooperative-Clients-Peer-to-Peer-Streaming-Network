package P2Pconsul

import (
	"fmt"
	"github.com/hashicorp/consul/api"
	hlpr "goDASH/P2Pconsul/HelperFunctions"
	pb "goDASH/P2Pconsul/P2PService"
	"goDASH/logging"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"log"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Collaborative Code - Start

type NodeUrl struct {
	//node variables

	ClientName string

	//current url being searched for
	url string

	//map of all previously retreived URLs
	previousUrl map[string]string

	//grpc address
	Addr string

	// http server port
	ContentPort string

	//directory location of http video content
	ContentLocation string

	//all known neighbors
	Clients map[string]string

	//IP address of device
	IP net.IP

	//determines if client is registered with consul
	Registered bool

	//determine if client should update consul
	update bool

	//determine if debug should be printed
	debug bool

	//consul variables
	SDAddress string
	SDKV      api.KV

	//debug details
	Debugfile string
	Debuglog  bool

	//Server for implementation
	pb.UnimplementedP2PServiceServer
}

func (n *NodeUrl) Initialisation() {
	//initialise required varibales
	n.Clients = make(map[string]string)
	n.Registered = false
	n.previousUrl = make(map[string]string)
	rand.Seed(time.Now().UnixNano())
	port := rand.Intn(63000) + 1023
	n.GetOutboundIP()
	n.Addr = n.IP.String() + ":" + strconv.Itoa(port)
	n.debug = false

	fmt.Printf("addr : %v\n", n.Addr)
	fmt.Printf(" Content addr : %v\n", n.ContentPort)
	fmt.Printf(" Content Location :%v\n", n.ContentLocation)
	fmt.Printf("IP ADdRESSS:%v\n", n.IP)
	//start server listening
	n.RegisterNode()

}

//Start the GRPC server listening on n.Addr. Takes waitgroup as input. Will return wg.done if terminates
func (n *NodeUrl) StartListening(wg *sync.WaitGroup) {
	lis, err := net.Listen("tcp", n.Addr)
	fmt.Printf("GRPC Server Listening on %v\n", n.Addr)
	if err != nil {
		log.Fatalf("failed to start listening %v", err)
	}

	_n := grpc.NewServer()

	pb.RegisterP2PServiceServer(_n, n)

	reflection.Register(_n)
	defer _n.Stop()
	if err := _n.Serve(lis); err != nil {
		fmt.Println("server failed")
		log.Fatalf("Server Failed to serve")
	}
	wg.Done()
}

//First register node with Consul server under a given URL
//set n.SDKV to reference Consul server connnection
func (n *NodeUrl) RegisterNode() {
	config := api.DefaultConfig()
	config.Address = n.SDAddress
	consul, err := api.NewClient(config)
	if err != nil {
		log.Panicln("Unable to register with KV Service Discovery\n")
	}

	//create Key Value store on consul server
	//Key is (URL+NodeAddress : NodeAddress)
	kv := consul.KV()

	//Store KV for later use
	n.SDKV = *kv
	fmt.Printf("Successfully registered : (%v : %v)\n", hlpr.HashSha(n.url)+n.Addr, []byte(n.Addr))
}

//search network for a given url
func (n *NodeUrl) Search(url string) string {
	//start timer of search fucntion
	start := time.Now()
	n.DebugPrint("in consul search url :" + url)
	//bool showing if desired file has been retreived
	notFound := true
	//split url
	l := strings.Split(url, "/")

	//retreive file location for download from split url
	location := l[len(l)-1]

	//set update consul to true
	n.update = true

	//hash searched URL to search the network for the hash of the URL desired
	key := hlpr.HashSha(url)

	n.DebugPrint("Start of consul search Location:" + location)

	//if desired content is not in current clients
	//search clients of clients
	if len(n.Clients[key]) != 0 {
		//if current client is known to have correct content from previous requests
		contentServer, err := n.GetContentServerAddress(n.Clients[key])
		if err != nil {
			return url
		} else {
			url = "http://" + contentServer + "/" + location + "::localclient"
			notFound = false
		}
	}

	//loop over all known nodes
	for _, client := range n.Clients {
		n.DebugPrint("Looping client check")
		//establish connection to client and check for content
		conn, err := grpc.Dial(client, grpc.WithInsecure())
		if err != nil {
			break
			log.Fatalf("Did not connect to server : %v\n", err)
		}

		defer conn.Close()
		//establish P2PServiceClient connection
		c := pb.NewP2PServiceClient(conn)

		//GRPC call to check clients for content
		downloadAddress, err := c.CheckClients(context.Background(), &pb.CheckRequest{Address: n.Addr, Target: key})

		if err != nil {
			n.DebugPrint("rpc error client check")

			continue
		}

		if downloadAddress.Addr != "nil" {
			//add relevant client to client list
			n.Clients[key] = downloadAddress.Addr

			//get content server address for Url for download
			contentServer, err := n.GetContentServerAddress(downloadAddress.Addr)
			if err != nil {
				n.DebugPrint("rpc error 2 client check")
				break
			}
			//construct URL for return using content server address and file location
			url = "http://" + contentServer + "/" + location + "::clients"

			notFound = false
			break
		}
	}

	//in Case not currently known Locally consult Consul Server
	if notFound {
		n.DebugPrint("checking consul")
		//retreive list of all Consul URL entries
		kvpairs, _, err := n.SDKV.List(key, nil)
		n.DebugPrint("checking consul too")
		if err != nil {
			n.DebugPrint("consul error")
			return url
		} else {
			//Loop Key Value pair matches query
			//randomly shuffle key value pairs
			n.DebugPrint("checking consul keys")
			for i := 1; i < len(kvpairs); i++ {
				r := rand.Intn(i + 1)
				if i != r {
					kvpairs[r], kvpairs[i] = kvpairs[i], kvpairs[r]
				}
			}

			for _, kventry := range kvpairs {
				//Check key isnt this node
				n.DebugPrint("Looping consul entries")
				if kventry.Key[0:len(key)] == key && kventry.Key != key+n.Addr {
					//add client address to list of known clients
					n.Clients[kventry.Key[0:len(key)]] = string(kventry.Value)

					//returned address of device with requested url content
					contentServer, err := n.GetContentServerAddress(string(kventry.Value))
					if err != nil {
						break
						log.Fatalf("Error Kventry : %v\n", err)
						fmt.Println("KV ERROR")
					} else {
						//construct URL from retreived server address and content location
						url = "http://" + contentServer + "/" + location + "::consul"
						fmt.Println("location" + location)
						notFound = false
						break
					}
				}
			}
		}
	}
	//return url hosting requested content along with time taken to retreive content
	return url + "::" + time.Since(start).String()
}

//Updates consul reference to this node
//updates nodes URL references also
func (n *NodeUrl) UpdateConsul(url string) {
	//add new consul entry
	n.DebugPrint(fmt.Sprintf("consul Update : %v\n", url+n.Addr))
	p := &api.KVPair{Key: url + n.Addr, Value: []byte(n.Addr)}
	_, err := n.SDKV.Put(p, nil)
	fmt.Println("updating consul ###############################################")
	n.DebugPrint(fmt.Sprintf("error update consul %v\n", err))
	if err != nil {
		n.DebugPrint("error update consul")
		fmt.Println("issue")
	}
	//update nodes url references
	n.previousUrl[n.url] = n.url
	n.url = url

}

//GRPC function that takes in requested URL and returns the URL of the requested content if the content is present on
//this device
func (n *NodeUrl) getContentAddr(address string) (serverAddr string) {
	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("Did not connect to server : %v\n", err)
	}
	defer conn.Close()
	c := pb.NewP2PServiceClient(conn)

	if err != nil {
		log.Fatalf("Did not connect to server : %v\n", err)
		fmt.Println(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	downloadAddress, err := c.GetServerAddr(ctx, &pb.ServerRequest{Address: n.Addr})
	return downloadAddress.Addr

}

//functions takes requests from other node to check their clients for desired content
func (n *NodeUrl) CheckClients(ctx context.Context, in *pb.CheckRequest) (*pb.CheckReply, error) {
	fmt.Printf("\nCheck Client Target: %v\n", n.Clients[in.Target])
	response := pb.CheckReply{Addr: "nil"}
	if len(n.previousUrl[in.Target]) > 0 {
		response = pb.CheckReply{Addr: n.Addr}
		fmt.Printf("N addr: %v\n", n.Addr)
		return &response, nil
	} else {
		if len(n.Clients[in.Target]) > 0 {
			response = pb.CheckReply{Addr: n.Clients[in.Target]}
		}
		return &response, nil
	}

}

//previously used second check function no longer in use!!
func (n *NodeUrl) SecondCheckLoop(url string) (addr string) {
	for _, client := range n.Clients {
		conn, err := grpc.Dial(client, grpc.WithInsecure())
		if err != nil {
			continue
			log.Fatalf("Did not connect to server : %v\n", err)
		}
		defer conn.Close()
		c := pb.NewP2PServiceClient(conn)

		response, err := c.SecondCheckClient(context.Background(), &pb.SecondCheckRequest{Url: url})
		if err != nil {
			fmt.Printf("Error in second check client%v\n", err)
			return "nil"
		}
		if response.Addr != "nil" {
			return response.Addr
		}
	}
	return "nil"
}

//previously used second check client function no longer in use
func (n *NodeUrl) SecondCheckClient(ctx context.Context, in *pb.SecondCheckRequest) (*pb.SecondCheckReply, error) {
	if len(n.previousUrl[in.Url]) > 0 || n.url == in.Url {
		response := pb.SecondCheckReply{Addr: n.Addr}
		return &response, nil
	} else {
		response := pb.SecondCheckReply{Addr: "nil"}
		return &response, nil
	}

}

//grpc function that return the address of the http server address for this device
func (n *NodeUrl) GetServerAddr(ctx context.Context, in *pb.ServerRequest) (*pb.ServerRequestReply, error) {
	response := pb.ServerRequestReply{Addr: n.IP.String() + n.ContentPort}
	return &response, nil
}

//returns http server address of device with GRPC address provided
func (n *NodeUrl) GetContentServerAddress(address string) (string, error) {
	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("Did not connect to server : %v\n", err)
		fmt.Println(err)
	}

	s := pb.NewP2PServiceClient(conn)

	if err != nil {
		log.Fatalf("Did not connect to server : %v\n", err)
		fmt.Println(err)
	}

	downloadAddress, err := s.GetServerAddr(context.Background(), &pb.ServerRequest{Address: address})
	fmt.Printf("download address %v\n", address)
	if err != nil { //Add random pick for which node to download from

		//add relevant node to clients
		fmt.Println(err)
		return "nil", err
	}
	url := downloadAddress.Addr
	return url, nil
}

//Starts this devices HTTP server serving the content in location directory on the provided port
func (n *NodeUrl) ContentServerStart(location string, port string, wg *sync.WaitGroup) {
	server := http.NewServeMux()

	//handlers that serve the home html file when called
	fs := http.FileServer(http.Dir(location))

	//handles paths by serving correct files
	//there will be if statements down here that check if someone has won or not soon
	server.Handle("/", fs)

	//logs that server is Listening
	log.Printf("Listening... %v\n", location)
	//starts server
	http.ListenAndServe(n.IP.String()+port, server)
	wg.Done()
}

//returns IP address of this device
func (n *NodeUrl) GetOutboundIP() {
	conn, err := net.Dial("udp", "10.0.0.1:80")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	n.IP = conn.LocalAddr().(*net.UDPAddr).IP

}

//sets variables for debug print
func (n *NodeUrl) SetDebug(DebugFile string, DebugLog bool) {
	n.Debugfile = DebugFile
	n.Debuglog = DebugLog
	n.debug = true
}

//print line to debug file
func (n *NodeUrl) DebugPrint(s string) {
	if n.debug {
		logging.DebugPrint(n.Debugfile, n.Debuglog, "\nDEBUG: ", s)
	}
}
