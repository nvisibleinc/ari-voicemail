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
	// "github.com/coopernurse/gorp"
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

/*	Voicemail Main
authenticate:
	voicemail_box
	pin
		check if valid
		true: func(leave), goto(main)
		false: decrement tries, goto(authenticate)

main:
	if new messages exist
		true: play(you have new messages)
		false: continue

1: new messages
2: change folders
	0 new
	1 old
	2 work
	3 family
	4 friends
	# cancel

3: advanced options
	4 outgoing call
		enter message to call, then press pound, * to cancel
	5 leave a message
		1 extension
		2 directory (won't implement)

0: mailbox options
	1 unavailable msg
	2 busy msg
	3 name
	4 temporary greeting
	5 password
	* main menu
*: help
#: exit
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

var (
	config       Config
	db           *sql.DB
	getMessages  *sql.Stmt
	getGreeting  *sql.Stmt
	insertNewMsg *sql.Stmt
)

type Config struct {
	MySQLURL     string      `json:"mysql_url"`
	Applications []string    `json:"applications"`
	MessageBus   string      `json:"message_bus"`
	BusConfig    interface{} `json:"bus_config"`
}

type vmInternal struct {
	Mailbox         string
	Retries         int
	ChannelID       string
	ActiveRecording string
	ActivePlaybacks []string
}

func (v *vmInternal) AddPlayback(id string) {
	v.ActivePlaybacks = append(v.ActivePlaybacks, id)
}

func (v *vmInternal) RemovePlayback(id string) {
	for i := range v.ActivePlaybacks {
		if v.ActivePlaybacks[i] == id {
			v.ActivePlaybacks = append(v.ActivePlaybacks[:i], v.ActivePlaybacks[i+1:]...)
			return
		}
	}
}
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
	getMessages, err = db.Prepare("SELECT * FROM voicemail_messages WHERE mailbox=? AND folder=?")
	if err != nil {
		log.Fatal(err)
	}
	getGreeting, err = db.Prepare("SELECT recording_id FROM voicemail_messages WHERE mailbox=? AND folder=?")
	if err != nil {
		log.Fatal(err)
	}
	insertNewMsg, err = db.Prepare("INSERT INTO voicemail_messages values (NULL, ?, ?, 'New', NULL, 0, ?)")
	if err != nil {
		log.Fatal(err)
	}
}

// startVMMainApp starts the primary voicemail application for retrieving messages.
func startVMMainApp(app string) {
	return
}

// startVMApp starts the primary voicemail application for leaving messages.
func startVMApp(app string) {
	fmt.Printf("Started application: %s", app)
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
				pb, _ = a.ChannelsPlay(vmState.ChannelID, "sounds:vm-intro")
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
		case "PlaybackFinished":
			var p ari.PlaybackFinished
			json.Unmarshal([]byte(event.ARI_Body), &p)
			vmState.RemovePlayback(p.Playback.Id)
			if len(vmState.ActivePlaybacks) == 0 {
				return leaveMessage, vmState
			}
			return introPlayed, vmState
		case "ChannelDtmfReceived":
			var c ari.ChannelDtmfReceived
			json.Unmarshal([]byte(event.ARI_Body), &c)
			switch c.Digit {
			case "#":
				playbacksStop(a, vmState)
				return leaveMessage, vmState
			}
		}
	}
	return introPlayed, vmState
}

func leaveMessage(a *ari.AppInstance, vmState *vmInternal) (vmstateFunc, *vmInternal) {
	messageID := ari.UUID()
	vmState.ActiveRecording = messageID
	a.ChannelsRecord(vmState.ChannelID, messageID, "ulaw", "", "", "", "true")
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

// DEPRECATED: ConsumeEvents pulls events off the channel and passes to the application.
func startAppHandler(a *ari.AppInstance) {
	// this is where you would hand off the information to your application
	for event := range a.Events {
		fmt.Println("got event")
		switch event.Type {
		case "StasisStart":
			var s ari.StasisStart
			json.Unmarshal([]byte(event.ARI_Body), &s)
			a.ChannelsAnswer(s.Channel.Id)
			fmt.Println("Got start message")
		case "ChannelDtmfReceived":
			var c ari.ChannelDtmfReceived
			fmt.Println("Got DTMF")
			json.Unmarshal([]byte(event.ARI_Body), &c)
			fmt.Printf("We got DTMF: %s\n", c.Digit)
			switch c.Digit {
			case "1":
				a.ChannelsPlay(c.Channel.Id, "sound:tt-monkeys", "en")
			case "2":
				a.ChannelsPlay(c.Channel.Id, "sound:tt-weasels")
			case "3":
				a.ChannelsPlay(c.Channel.Id, "sound:demo-congrats")
			case "4":
				err := a.MailboxesUpdate("1234@test", 0, 0)
				if err != nil {
					fmt.Println(err)
				}
			case "5":
				m, err := a.MailboxesGet("1234@test")
				if err != nil {
					fmt.Println(err)
				} else {
					fmt.Printf("Mailbox info is: %v", m)
				}
			}
		case "ChannelHangupRequest":
			fmt.Println("Channel hung up")
		case "StasisEnd":
			fmt.Println("Got end message")
		}
	}
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
	fmt.Println("Welcome to the go-ari-client")
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
