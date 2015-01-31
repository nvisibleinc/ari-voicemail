package main


import (
	"encoding/json"
	"fmt"
//	"io/ioutil"
//	"log"
	"strings"
	"time"

//	"database/sql"
//	_ "github.com/go-sql-driver/mysql"
	"go-ari-library"
)

// vmMainInternal struct holds information about the internal state of a running voicemail application instance.
type vmMainInternal struct {
	Mailbox         string
	Domain			string
	Password		string
	Retries         int
	ChannelID       string
	ActiveRecording string
	ActivePlaybacks []string
}

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

// New stuff that we need to figure out and clean up
type vmMainStateFunc func(a *ari.AppInstance, vmState *vmMainInternal) (vmMainStateFunc, *vmMainInternal)

// AddPlayback adds a playback to the string slice that holds the list of file IDs of the in-flight playback files.
func (v *vmMainInternal) AddPlayback(id string) {
	v.ActivePlaybacks = append(v.ActivePlaybacks, id)
}

// RemovePlayback removes the file ID of the in-flight playback files as they have been played to the channel.
func (v *vmMainInternal) RemovePlayback(id string) {
	for i := range v.ActivePlaybacks {
		if v.ActivePlaybacks[i] == id {
			v.ActivePlaybacks = append(v.ActivePlaybacks[:i], v.ActivePlaybacks[i+1:]...)
			return
		}
	}
}

// startVMMainApp starts the primary voicemail application for retrieving messages.
func startVMMainApp(app string) {
	fmt.Printf("Started application: %s\n", app)
	application := new(ari.App)
	application.Init(app, startVMMainHandler)
	select {
	case <-application.Stop:
		return
	}
}

// startVMMainHandler initializes a new voicemail main application instance
func startVMMainHandler(a *ari.AppInstance) {
	v := &vmMainInternal{Retries: 0, Mailbox: "", Domain: "", Password: ""}
	state, vmMainState := vmMainStartState(a, v)
	for state != nil {
		state, vmMainState = state(a, vmMainState)
	}
	fmt.Println("exiting app instance")
}

func vmMainStartState(a *ari.AppInstance, vmMainState *vmMainInternal) (vmMainStateFunc, *vmMainInternal) {
	select {
	case event := <-a.Events:
		switch event.Type {
		case "StasisStart":
			fmt.Println("Got start message")
			var s ari.StasisStart
			json.Unmarshal([]byte(event.ARI_Body), &s)
			vmMainState.ChannelID = s.Channel.Id
			a.ChannelsAnswer(vmMainState.ChannelID)
			fmt.Printf("Args is: %s\n", s.Args)
			for i, v := range s.Args {
				switch i {
				case 0:
					fmt.Println("Case 0")
					if v != "" {
						fmt.Printf("Domain is: %s\n", v)
						vmMainState.Domain = strings.TrimSpace(v)
					} else {
						return nil, vmMainState	
					}
				case 1:
					fmt.Println("Case 0")
					if v != "" {
						fmt.Printf("Mailbox is: %s\n", v)
						vmMainState.Mailbox = strings.TrimSpace(v)
					} else { 
						pb, _ := a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-login")
						vmMainState.AddPlayback(pb.Id)
						return acceptMbox, vmMainState
					}
				}
			}
			if vmMainState.Domain == "" {
				return hangupVMMain, vmMainState
			}
			if vmMainState.Mailbox != "" && vmMainState.Domain != "" {
				pb, _ := a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-password")
				vmMainState.AddPlayback(pb.Id)
				return acceptPassword, vmMainState
			} else {
				pb, _ := a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-login")
				vmMainState.AddPlayback(pb.Id)
				return acceptMbox, vmMainState
			}
		}
	}
	return vmMainStartState, vmMainState
}

func acceptMbox(a *ari.AppInstance, vmMainState *vmMainInternal) (vmMainStateFunc, *vmMainInternal) {
	select {
	case event := <-a.Events:
		switch event.Type {
		case "ChannelDtmfReceived":
			var c ari.ChannelDtmfReceived
			json.Unmarshal([]byte(event.ARI_Body), &c)
			switch c.Digit {
			case "#":
				a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-password")
				return acceptPassword, vmMainState
			default:
				vmMainState.Mailbox = strings.Join([]string{vmMainState.Mailbox, c.Digit}, "")
				return acceptMbox, vmMainState
			}
		}
	}
	return acceptMbox, vmMainState
}

func acceptPassword(a *ari.AppInstance, vmMainState *vmMainInternal) (vmMainStateFunc, *vmMainInternal) {
	select {
	case event := <-a.Events:
		switch event.Type {
		case "ChannelDtmfReceived":
			var c ari.ChannelDtmfReceived
			json.Unmarshal([]byte(event.ARI_Body), &c)
			switch c.Digit {
			case "#":
				fmt.Println("Got a hash to end password input")
				if authorizeUser(vmMainState.Mailbox, vmMainState.Domain, vmMainState.Password) {
					return vmMainMenu, vmMainState
				} else {
					vmMainState.Retries++
					vmMainState.Password = ""
					a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-incorrect")
					if vmMainState.Retries < 3 {
						a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-password")
						return acceptPassword, vmMainState
					} else {
						fmt.Println("Should be playing goodbye")
						a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-goodbye")
						return hangupVMMain, vmMainState
					}
				}
			default:
				vmMainState.Password = strings.Join([]string{vmMainState.Password, c.Digit}, "")
				fmt.Printf("Password is now: %s\n", vmMainState.Password)
				return acceptPassword, vmMainState
			}
		}
	}
	return acceptPassword, vmMainState
}

func hangupVMMain(a *ari.AppInstance, vmMainState *vmMainInternal) (vmMainStateFunc, *vmMainInternal) {
	time.Sleep(3 * time.Second)
	a.ChannelsHangup(vmMainState.ChannelID)
	return nil, vmMainState
}

func vmMainMenu(a *ari.AppInstance, vmMainState *vmMainInternal) (vmMainStateFunc, *vmMainInternal) {
	return nil, vmMainState
}

func authorizeUser(mailbox, domain, password string) bool {
	fmt.Printf("Trying to authorize user Z%sZ for domain Z%sZ with password Z%sZ", mailbox, domain, password)
	var mbx string
	db.Ping()
	rows, err :=checkPass.Query(mailbox, domain, password)
	if err != nil {
		fmt.Println(err)
		return false
	}
	for rows.Next() {
		err = rows.Scan(&mbx)
		if err != nil {
			return false
		}
		fmt.Printf("returned mailbox is %s\n", mbx)
		if mbx == mailbox {
			fmt.Println("Password OK")
			return true
		}
	}
	return false
}
