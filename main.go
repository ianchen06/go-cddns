package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

var logger *log.Logger

func init() {
	logger = log.New(io.Writer(os.Stdout), "go-cddns:", log.Lshortfile)
}

func main() {

	// Parse command line flags
	opts := parseCommandLineFlags()

	//	Read config file
	file, err := ioutil.ReadFile(opts.Filename)
	if err != nil {
		logger.Fatalf("Cannot open config file %s", opts.Filename)
	}

	var cloudflareConfig config
	err = json.Unmarshal(file, &cloudflareConfig)
	if err != nil {
		logger.Fatalln(err)
	}
	cloudflareConfig.CloudflareURL = "https://api.cloudflare.com/client/v4/zones/"

	err = validateConfig(&cloudflareConfig)
	if err != nil {
		logger.Fatalln(err)
	}

	minimalTLSConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	tlsTransport := &http.Transport{
		TLSClientConfig: minimalTLSConfig,
	}
	client := &http.Client{
		Transport: tlsTransport,
	}

	cfm := &cloudflareManager{
		cloudflareConfig,
		client,
		"",
	}

	// Register interrupt handler
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go handleInterrupt(sigs, cfm)

	updateCloudflareRecord(cfm)

	logger.Println("done")
}

func handleInterrupt(signalChannel chan os.Signal, cfm *cloudflareManager) {
	<-signalChannel
	logger.Println("Shutting down, performing cleanup operations")
	if cfm.Remove {
		logger.Println("Removing DNS records")
		records, err := cfm.GetDNSRecords()
		if err != nil {
			logger.Fatalln(err)
		}

		for _, name := range cfm.RecordNames {
			if record, ok := records[name]; ok {
				logger.Printf("Deleting %v", record.Name)
				cfm.DeleteDNSRecord(record)
			}
		}

	}
	logger.Println("Exiting")
	os.Exit(0)
}

func updateCloudflareRecord(cfm *cloudflareManager) {

	// Get an updated record
	updatedIPAddress, err := getCurrentIPAddress(cfm.Client)
	if err != nil {
		logger.Println(err)
		return
	}

	if updatedIPAddress != cfm.CurrentIPAddress {
		logger.Printf("IP Address changed from %v, to %v\n", cfm.CurrentIPAddress, updatedIPAddress)
		cfm.CurrentIPAddress = updatedIPAddress

		//	Get the cloudflare DNS records
		records, err := cfm.GetDNSRecords()
		if err != nil {
			logger.Println(err)
			return
		}
		//	Range over the given RecordNames and update them, if possible
		for _, name := range cfm.RecordNames {
			if record, ok := records[name]; ok {
				if record.Content != cfm.CurrentIPAddress {
					logger.Printf("Record %v has ip address %v, updating to %v\n", name, record.Content, cfm.CurrentIPAddress)
					cfm.updateDNSRecord(record, cfm.CurrentIPAddress)
				} else {
					logger.Printf("Record %v has correct IP Address, skipping\n", name)
				}
			} else {
				//	Record doesn't exist, create it
				logger.Printf("Record %v doesn't exist, creating it with IP Address: %v\n", name, cfm.CurrentIPAddress)
				cfm.CreateDNSRecord(name, A, cfm.CurrentIPAddress)
			}
		}
	}
}

func getCurrentIPAddress(httpClient *http.Client) (string, error) {
	resp, err := httpClient.Get("http://bot.whatismyipaddress.com")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	ipAddress, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(ipAddress), nil

}

func validateConfig(cloudflareConfig *config) error {

	// Validate credentials
	if cloudflareConfig.Key == "" {
		return errors.New("Empty API Key, exiting")
	}

	if cloudflareConfig.Email == "" {
		return errors.New("Empty Email Address, exiting")
	}

	if cloudflareConfig.DomainName == "" {
		return errors.New("Empty Domain Name, exiting")
	}

	if len(cloudflareConfig.RecordNames) == 0 {
		return errors.New("No records to update, exiting")
	}

	if cloudflareConfig.UpdateInterval < 5 {
		return errors.New("Update interval cannot be less than 5 minutes")
	}

	logger.Printf("Updating records for: %v", cloudflareConfig.RecordNames)

	//	Check for correct config values
	logger.Printf("Setting update interval to %v minutes\n", cloudflareConfig.UpdateInterval)

	return nil
}
