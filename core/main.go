package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
)

var version = "2.6.3"

func main() {
	// "midos-tunnel panel ..." runs the web panel; anything else is a relay
	if len(os.Args) > 1 && os.Args[1] == "panel" {
		panelMain(os.Args[2:])
		return
	}
	// "midos-tunnel migrate" moves an older install onto the current names (migrate.go)
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		migrateMain()
		return
	}
	cfgPath := flag.String("config", "/etc/Midos-tunnel/veil.json", "path to the JSON configuration")
	showVer := flag.Bool("version", false, "print the version and exit")
	check := flag.Bool("check", false, "validate the configuration and exit")
	flag.Parse()

	if *showVer {
		log.SetFlags(0)
		log.Printf("midos-tunnel %s", version)
		return
	}
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("tunnel: ")

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("configuration: %v", err)
	}
	if _, err := cfg.parsePorts(); err != nil {
		log.Fatalf("configuration: %v", err)
	}
	if _, err := cfg.parseUDPPorts(); err != nil {
		log.Fatalf("configuration: udp: %v", err)
	}
	if *check {
		log.Printf("configuration is valid (mode=%s)", cfg.Mode)
		return
	}

	// A clean exit matters: systemd restarts us, and a half-closed listener
	// would leave the port unusable for the next start.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		log.Printf("shutting down")
		os.Exit(0)
	}()

	switch cfg.Mode {
	case "server":
		err = runServer(cfg)
	case "client":
		err = runClient(cfg)
	}
	if err != nil {
		log.Fatalf("%s mode stopped: %v", cfg.Mode, err)
	}
}
