// Package cmd contains an entrypoint for running an ion-sfu instance.
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"unsafe"

	"github.com/gorilla/websocket"
	log "github.com/pion/ion-log"
	"github.com/pion/ion-sfu/cmd/signal/json-rpc/server"
	"github.com/pion/ion-sfu/pkg/middlewares/datachannel"
	"github.com/pion/ion-sfu/pkg/sfu"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sourcegraph/jsonrpc2"
	websocketjsonrpc2 "github.com/sourcegraph/jsonrpc2/websocket"
	"github.com/spf13/viper"
)

var (
	conf        = sfu.Config{}
	file        string
	cert        string
	key         string
	addr        string
	metricsAddr string
)

const (
	portRangeLimit = 100
)

func showHelp() {
	fmt.Printf("Usage:%s {params}\n", os.Args[0])
	fmt.Println("      -c {config file}")
	fmt.Println("      -cert {cert file}")
	fmt.Println("      -key {key file}")
	fmt.Println("      -a {listen addr}")
	fmt.Println("      -h (show help info)")
}

func load() bool {
	_, err := os.Stat(file)
	if err != nil {
		return false
	}

	viper.SetConfigFile(file)
	viper.SetConfigType("toml")

	err = viper.ReadInConfig()
	if err != nil {
		fmt.Printf("config file %s read failed. %v\n", file, err)
		return false
	}
	err = viper.GetViper().Unmarshal(&conf)
	if err != nil {
		fmt.Printf("sfu config file %s loaded failed. %v\n", file, err)
		return false
	}

	if len(conf.WebRTC.ICEPortRange) > 2 {
		fmt.Printf("config file %s loaded failed. range port must be [min,max]\n", file)
		return false
	}

	if len(conf.WebRTC.ICEPortRange) != 0 && conf.WebRTC.ICEPortRange[1]-conf.WebRTC.ICEPortRange[0] < portRangeLimit {
		fmt.Printf("config file %s loaded failed. range port must be [min, max] and max - min >= %d\n", file, portRangeLimit)
		return false
	}

	fmt.Printf("config %s load ok!\n", file)
	return true
}

func parse() bool {
	flag.StringVar(&file, "c", "config.toml", "config file")
	flag.StringVar(&cert, "cert", "", "cert file")
	flag.StringVar(&key, "key", "", "key file")
	flag.StringVar(&addr, "a", ":7000", "address to use")
	flag.StringVar(&metricsAddr, "m", ":8100", "merics to use")
	help := flag.Bool("h", false, "help info")
	flag.Parse()
	if !load() {
		return false
	}

	if *help {
		return false
	}
	return true
}

func startMetrics(addr string) {
	// start metrics server
	m := http.NewServeMux()
	m.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{
		Handler: m,
	}

	metricsLis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Panicf("cannot bind to metrics endpoint %s. err: %s", addr, err)
	}
	log.Infof("Metrics Listening at %s", addr)

	err = srv.Serve(metricsLis)
	if err != nil {
		log.Errorf("debug server stopped. got err: %s", err)
	}
}

type ExtendedPeer struct {
	sfu.Peer
	Name string
}

func main() {
	if !parse() {
		showHelp()
		os.Exit(-1)
	}

	fixByFile := []string{"asm_amd64.s", "proc.go", "icegatherer.go", "jsonrpc2"}
	fixByFunc := []string{"Handle"}
	log.Init(conf.Log.Level, fixByFile, fixByFunc)

	log.Infof("--- Starting SFU Node ---")

	s := sfu.NewSFU(conf)
	dc := s.NewDatachannel(sfu.APIChannelLabel)
	dc.Use(datachannel.SubscriberAPI)

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	http.Handle("/ws", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			panic(err)
		}
		defer c.Close()

		user := r.URL.Query().Get("user")
		log.Infof("Connected %v", user)
		var extendedPeer ExtendedPeer = ExtendedPeer{*sfu.NewPeer(s), user}
		var convertedSfuPointer *sfu.Peer = (*sfu.Peer)(unsafe.Pointer(&extendedPeer))
		p := server.NewJSONSignal(convertedSfuPointer)
		defer p.Close()

		jc := jsonrpc2.NewConn(r.Context(), websocketjsonrpc2.NewObjectStream(c), p)
		<-jc.DisconnectNotify()
	}))

	http.Handle("/users", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionId := r.URL.Query().Get("session")
		session, _ := s.GetSession(sessionId)
		for _, peer := range session.Peers() {
			var extendedPeer *ExtendedPeer = (*ExtendedPeer)(unsafe.Pointer(peer))
			log.Infof("Peer %v %v", extendedPeer.Name, extendedPeer.Publisher().GetRouter().ID())
		}
	}))

	go startMetrics(metricsAddr)

	var err error
	if key != "" && cert != "" {
		log.Infof("Listening at https://[%s]", addr)
		err = http.ListenAndServeTLS(addr, cert, key, nil)
	} else {
		log.Infof("Listening at http://[%s]", addr)
		err = http.ListenAndServe(addr, nil)
	}
	if err != nil {
		panic(err)
	}
}
