package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"database/sql"
	_ "github.com/go-sql-driver/mysql"
	"go-ari-library"
)

/* Voicemail Users
id			(auto)
mailbox 	(varchar)
domain		(varchar)
pin			(varchar)
email		(varchar)
first_name	(varchar)
last_name	(varchar)
*/

/* Voicemail Messages
id 		(auto)
mailbox (varchar)
domain	(varchar)
folder	(varchar)
timestamp	(date)
read		(bool)
recording_id	(varchar)
*/

/* Voicemail
Get mailbox
Play unavailable / busy message
	check if should play temporary greeting
	if no message recorded, try name
		if no name, play generic message

Leave message
	Record message
	1 to accept this recording
	2 to listen to it
	3 to re-record this message
	* help
*/

// setup global variables
var (
	config       Config
	db           *sql.DB
	getMessages  *sql.Stmt
	getGreeting  *sql.Stmt
	insertNewMsg *sql.Stmt
	checkPass    *sql.Stmt
)

// Config struct contains the variable configuration options from the config file.
type Config struct {
	MySQLURL     string      `json:"mysql_url"`
	Applications []string    `json:"applications"`
	MessageBus   string      `json:"message_bus"`
	BusConfig    interface{} `json:"bus_config"`
}

// vmInternal struct holds information about the internal state of a running voicemail application instance.
type vmInternal struct {
	Mailbox         string
	Retries         int
	ChannelID       string
	ActiveRecording string
	ActivePlaybacks []string
}

// AddPlayback adds a playback to the string slice that holds the list of file IDs of the in-flight playback files.
func (v *vmInternal) AddPlayback(id string) {
	v.ActivePlaybacks = append(v.ActivePlaybacks, id)
}

// RemovePlayback removes the file ID of the in-flight playback files as they have been played to the channel.
func (v *vmInternal) RemovePlayback(id string) {
	for i := range v.ActivePlaybacks {
		if v.ActivePlaybacks[i] == id {
			v.ActivePlaybacks = append(v.ActivePlaybacks[:i], v.ActivePlaybacks[i+1:]...)
			return
		}
	}
}

// initialize the client side configuration and setup.
func init() {
	var err error

	// parse the configuration file and get data from it
	configpath := flag.String("config", "./config_client.json", "Path to config file")
	flag.Parse()
	configfile, err := ioutil.ReadFile(*configpath)
	if err != nil {
		log.Fatal(err)
	}

	// read in the configuration file and unmarshal the json, storing it in 'config'
	json.Unmarshal(configfile, &config)

	db, err = sql.Open("mysql", config.MySQLURL)
	if err != nil {
		log.Fatal(err)
	}

	// retrieve the messages for the current mailbox and folder
	getMessages, err = db.Prepare("SELECT * FROM voicemail_messages WHERE mailbox=? AND folder=?")
	if err != nil {
		log.Fatal(err)
	}

	// get the recording_id containing the appropriate greeting for the current mailbox and folder
	getGreeting, err = db.Prepare("SELECT recording_id FROM voicemail_messages WHERE mailbox=? AND folder=?")
	if err != nil {
		log.Fatal(err)
	}

	// insert a new message into the list of voicemail messages for the New folder
	insertNewMsg, err = db.Prepare("INSERT INTO voicemail_messages values (NULL, ?, ?, 'New', NULL, 0, ?)")
	if err != nil {
		log.Fatal(err)
	}
	
	// check if a password is correct for the provided mailbox and domain
	checkPass, err = db.Prepare("SELECT mailbox from voicemail_users where mailbox=? and domain=? and pin=SHA2(?, 256)")
	if err != nil {
		log.Fatal(err)
	}
}

// startVMApp starts the primary voicemail application for leaving messages.
func startVMApp(app string) {
	fmt.Printf("Started application: %s\n", app)
	application := new(ari.App)
	application.Init(app, startVMHandler)
	select {
	case <-application.Stop:
		return
	}
}

// New stuff that we need to figure out and clean up
type vmstateFunc func(a *ari.AppInstance, vmState *vmInternal) (vmstateFunc, *vmInternal)

// startVMHandler initializes a new voicemail application instance
func startVMHandler(a *ari.AppInstance) {
	v := new(vmInternal)
	state, vmState := vmstartState(a, v)
	for state != nil {
		state, vmState = state(a, vmState)
	}
	fmt.Println("exiting app instance")
}

func vmstartState(a *ari.AppInstance, vmState *vmInternal) (vmstateFunc, *vmInternal) {
	select {
	case event := <-a.Events:
		switch event.Type {
		case "StasisStart":
			fmt.Println("Got start message")
			var s ari.StasisStart
			json.Unmarshal([]byte(event.ARI_Body), &s)
			vmState.ChannelID = s.Channel.Id
			a.ChannelsAnswer(vmState.ChannelID)
			g := getGreetURI(s.Args[0], "unavailable")
			fmt.Printf("Greeting URI is %s\n", g)
			if strings.HasPrefix(g, "digits") {
				fmt.Println("Has digits")
				pb, _ := a.ChannelsPlay(vmState.ChannelID, "sound:vm-theperson", "en")
				vmState.AddPlayback(pb.Id)
				pb, _ = a.ChannelsPlay(vmState.ChannelID, g, "en")
				vmState.AddPlayback(pb.Id)
				pb, _ = a.ChannelsPlay(vmState.ChannelID, "sound:vm-isunavail", "en")
				vmState.AddPlayback(pb.Id)
				pb, _ = a.ChannelsPlay(vmState.ChannelID, "sound:vm-intro")
				vmState.AddPlayback(pb.Id)
			} else {
				a.ChannelsPlay(vmState.ChannelID, g)
			}

			vmState.Mailbox = s.Args[0]
			vmState.Retries = 0
			return introPlayed, vmState
		}
	}
	return vmstartState, vmState
}

func introPlayed(a *ari.AppInstance, vmState *vmInternal) (vmstateFunc, *vmInternal) {
	select {
	case event := <-a.Events:
		switch event.Type {
		case "PlaybackFinished":				fmt.Println("Got an octothorpe")
			var p ari.PlaybackFinished
			json.Unmarshal([]byte(event.ARI_Body), &p)
			vmState.RemovePlayback(p.Playback.Id)
			if len(vmState.ActivePlaybacks) == 0 {
				return startRecording, vmState
			}
			return introPlayed, vmState
		case "ChannelDtmfReceived":
			var c ari.ChannelDtmfReceived
			json.Unmarshal([]byte(event.ARI_Body), &c)
			switch c.Digit {
				case "#":
				playbacksStop(a, vmState)
				return startRecording, vmState
			}
		}
	}
	return introPlayed, vmState
}

func startRecording(a *ari.AppInstance, vmState *vmInternal) (vmstateFunc, *vmInternal) {
	fmt.Println("entered startRecording")
	messageID := ari.UUID()
	vmState.ActiveRecording = messageID
	pb, _ := a.ChannelsPlay(vmState.ChannelID, "sound:beep")
	vmState.AddPlayback(pb.Id)
	a.ChannelsRecord(vmState.ChannelID, messageID, "ulaw")
	return leaveMessage, vmState
}

func leaveMessage(a *ari.AppInstance, vmState *vmInternal) (vmstateFunc, *vmInternal) {

	fmt.Println("entered leaveMessage")
	select {
	case event := <-a.Events:
		switch event.Type {
		case "ChannelDtmfReceived":
			var c ari.ChannelDtmfReceived
			fmt.Println("Got DTMF")
			json.Unmarshal([]byte(event.ARI_Body), &c)
			fmt.Printf("We got DTMF: %s\n", c.Digit)
			switch c.Digit {
			case "#":
				a.RecordingsStop(vmState.ActiveRecording)
				pb, _ := a.ChannelsPlay(vmState.ChannelID, "sound:vm-review")
				vmState.AddPlayback(pb.Id)
				return listenMessage, vmState
			}
		case "ChannelHangupRequest":
			var c ari.ChannelHangupRequest
			json.Unmarshal([]byte(event.ARI_Body), &c)
			a.RecordingsStop(vmState.ActiveRecording)
			saveMessage(vmState)
			return hangupVM, vmState
		}
	}
	return leaveMessage, vmState
}

func listenMessage(a *ari.AppInstance, vmState *vmInternal) (vmstateFunc, *vmInternal) {
	select {
	case event := <-a.Events:
		//menuMaxTimesThrough := 3
		switch event.Type {
		case "ChannelDtmfReceived":
			var c ari.ChannelDtmfReceived
			json.Unmarshal([]byte(event.ARI_Body), &c)
			switch c.Digit {
			case "1":
				playbacksStop(a, vmState)
				saveMessage(vmState)
				return hangupVM, vmState
			case "2":
				playbacksStop(a, vmState)
				pb, _ := a.ChannelsPlay(vmState.ChannelID, strings.Join([]string{"recording:", vmState.ActiveRecording}, ""))
				vmState.AddPlayback(pb.Id)
			case "3":
				playbacksStop(a, vmState)
				a.RecordingsDeleteStored(vmState.ActiveRecording)
				return leaveMessage, vmState
			case "*":
				playbacksStop(a, vmState)
				pb, _ := a.ChannelsPlay(vmState.ChannelID, "sound:vm-review")
				vmState.AddPlayback(pb.Id)
			}
		}
	case <-time.After(10 * time.Second):
		playbacksStop(a, vmState)
		pb, _ := a.ChannelsPlay(vmState.ChannelID, "sound:vm-review")
		vmState.AddPlayback(pb.Id)
	}
	return listenMessage, vmState
}

func hangupVM(a *ari.AppInstance, vmState *vmInternal) (vmstateFunc, *vmInternal) {
	a.ChannelsHangup(vmState.ChannelID)
	return nil, vmState
}
func playbacksStop(a *ari.AppInstance, vmState *vmInternal) {
	for _, val := range vmState.ActivePlaybacks {
		a.PlaybacksStop(val)
		vmState.RemovePlayback(val)
	}
}

// getGreetURI returns the recording ID for the mailbox playback.
// Returns a recording: <greetingID> if a recording URI is available.
// Returns a digits: <mailbox> value if no recording was available.
func getGreetURI(mailbox string, greetType string) string {
	var greetingID string
	db.Ping()
	rows, err := getGreeting.Query(mailbox, greetType)
	if err != nil {
		fmt.Println(err)
		return strings.Join([]string{"digits:", mailbox}, "")
	}
	for rows.Next() {
		err = rows.Scan(&greetingID)
		fmt.Printf("greetingID is %s\n", greetingID)
		if err != nil || greetingID == "" {
			return strings.Join([]string{"digits:", mailbox}, "")
		}
	}
	if greetingID == "" {
		return strings.Join([]string{"digits:", mailbox}, "")
	}
	return strings.Join([]string{"recording:", greetingID}, "")
}

func saveMessage(vmState *vmInternal) error {
	insertNewMsg.Exec(vmState.Mailbox, "example.com", vmState.ActiveRecording);
	return nil;
}
// signalCatcher is a function to allows us to stop the application through an
// operating system signal.
func signalCatcher() {
	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGINT)
	sig := <-ch
	log.Printf("Signal received: %v", sig)
	os.Exit(0)
}

func main() {
	fmt.Println("Welcome to the ARI voicemail written in GO")
	ari.InitBus(config.MessageBus, config.BusConfig)

	for _, app := range config.Applications {
		// create consumer that uses the inboundEvents and parses them onto the parsedEvents channel
		switch app {
		case "voicemail":
			go startVMApp(app)
		case "voicemailmain":
			go startVMMainApp(app)
		}
	}

	go signalCatcher() // listen for os signal to stop the application
	select {}
}
