package main


import (
	"encoding/json"
	"fmt"
//	"io/ioutil"
//	"log"
	"strings"
	"strconv"
	"time"

//	"database/sql"
//	_ "github.com/go-sql-driver/mysql"
	"go-ari-library"
)

// vmMainInternal struct holds information about the internal state of a running voicemail application instance.
type vmMainInternal struct {
	Mailbox         string
	Domain			string
	CurrentFolder	string
	Password		string
	Retries         int
	ChannelID       string
	ActiveRecording string
	ActivePlaybacks []string
	MailboxProvided	bool
	NewMessageCount	int
	OldMessageCount	int
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

// PlaybacksStop stops all of the active playbacks and removes them from the list of active playbacks.
func (v *vmMainInternal) PlaybacksStop(a *ari.AppInstance) {
	for _, val := range v.ActivePlaybacks {
		a.PlaybacksStop(val)
		v.RemovePlayback(val)
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
	v := &vmMainInternal{Retries: 0, Mailbox: "", Domain: "", Password: "", CurrentFolder: "New", MailboxProvided: false}
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
					fmt.Println("Case 1")
					if v != "" {
						fmt.Printf("Mailbox is: %s\n", v)
						vmMainState.Mailbox = strings.TrimSpace(v)
						vmMainState.MailboxProvided = true
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
				a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-password")
				return acceptPassword, vmMainState
			} else {
				a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-login")
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
				if authorizeUser(vmMainState.Mailbox, vmMainState.Domain, vmMainState.Password) {
					vmMainState.Retries = 0
					return vmMainMenuIntro, vmMainState
				} else {
					vmMainState.Retries++
					vmMainState.Password = ""
					if vmMainState.Retries < 3 {
						if vmMainState.MailboxProvided {
							a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-incorrect")
							a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-password")
							return acceptPassword, vmMainState
						} else {
							a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-incorrect-mailbox")
							vmMainState.Mailbox = ""
							return acceptMbox, vmMainState
						}
					} else {
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

func (v *vmMainInternal) PlayMessageCounts(a *ari.AppInstance) {
	curFolder := v.CurrentFolder
	v.NewMessageCount = getMessageCount(v.Mailbox, v.Domain, "New")
	v.OldMessageCount = getMessageCount(v.Mailbox, v.Domain, "Old")
	pb, _ := a.ChannelsPlay(v.ChannelID, "sound:vm-youhave")
	v.AddPlayback(pb.Id)
	if !(v.NewMessageCount > 0 || v.OldMessageCount > 0) {
		pb, _ := a.ChannelsPlay(v.ChannelID, "sound:no")
		v.AddPlayback(pb.Id)
		pb, _ = a.ChannelsPlay(v.ChannelID, "sound:vm-messages")
		v.AddPlayback(pb.Id)
		return
	}
	switch  {
	case v.NewMessageCount == 1:
		pb, _ := a.ChannelsPlay(v.ChannelID, "sound:one")
		v.AddPlayback(pb.Id)
		v.PlayFolder(a, "New")
		pb, _ = a.ChannelsPlay(v.ChannelID, "sound:vm-message")
		v.AddPlayback(pb.Id)
	case v.NewMessageCount > 1 :
		pb, _ := a.ChannelsPlay(v.ChannelID, strings.Join([]string{"sound:digits/", strconv.Itoa(v.NewMessageCount)}, ""))
		v.AddPlayback(pb.Id)
		v.PlayFolder(a, "New")
		pb, _ = a.ChannelsPlay(v.ChannelID, "sound:vm-messages")
		v.AddPlayback(pb.Id)
	}
	if v.NewMessageCount != 0 && v.OldMessageCount != 0 {
		pb, _ := a.ChannelsPlay(v.ChannelID, "sound:vm-and")
		v.AddPlayback(pb.Id)
	}

	switch {
	case v.OldMessageCount == 1:
		pb, _ := a.ChannelsPlay(v.ChannelID, "sound:one")
		v.AddPlayback(pb.Id)
		v.PlayFolder(a, "Old")
		pb, _ = a.ChannelsPlay(v.ChannelID, "sound:vm-message")
		v.AddPlayback(pb.Id)
	case v.OldMessageCount > 1 :
		pb, _ := a.ChannelsPlay(v.ChannelID, strings.Join([]string{"sound:digits/", strconv.Itoa(v.OldMessageCount)}, ""))
		v.AddPlayback(pb.Id)
		v.PlayFolder(a, "Old")
		pb, _ = a.ChannelsPlay(v.ChannelID, "sound:vm-messages")
		v.AddPlayback(pb.Id)
	}
	v.CurrentFolder = curFolder
}

func (v *vmMainInternal) PlayFolder(a *ari.AppInstance, folder string) {
	var pb *ari.Playback
	switch folder {
	case "New":
		pb, _ = a.ChannelsPlay(v.ChannelID, "sound:vm-INBOX")
	case "Old":
		pb, _ = a.ChannelsPlay(v.ChannelID, "sound:vm-Old")
	case "Work":
		pb, _ = a.ChannelsPlay(v.ChannelID, "sound:vm-Work")
	}
	v.AddPlayback(pb.Id)
}

func vmMainMenuIntro(a *ari.AppInstance, vmMainState *vmMainInternal) (vmMainStateFunc, *vmMainInternal) {
	var pb *ari.Playback
	vmMainState.PlayMessageCounts(a)
	fmt.Printf("Message counts are New: %d   Old: %d\n", vmMainState.NewMessageCount, vmMainState.OldMessageCount)
	if (vmMainState.NewMessageCount > 0 || vmMainState.OldMessageCount > 0) {
		pb, _ = a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-onefor")
		vmMainState.AddPlayback(pb.Id)
	}
	if vmMainState.NewMessageCount > 0 {
		vmMainState.PlayFolder(a, "New")
		pb, _ = a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-messages")
		vmMainState.AddPlayback(pb.Id)
	} else if vmMainState.OldMessageCount > 0 {
		vmMainState.PlayFolder(a, "Old")
		pb, _ = a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-messages")
		vmMainState.AddPlayback(pb.Id)
	}

	pb, _ = a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-opts")
	vmMainState.AddPlayback(pb.Id)
	return vmMainMenu, vmMainState
}

func vmMainMenu(a *ari.AppInstance, vmMainState *vmMainInternal) (vmMainStateFunc, *vmMainInternal) {
	fmt.Println("Entered Main Menu")
	select {
	case event := <-a.Events:
		fmt.Printf("Event type is %s\n", event.Type)
		switch event.Type {
		case "ChannelDtmfReceived":
			vmMainState.PlaybacksStop(a)
			var c ari.ChannelDtmfReceived
			json.Unmarshal([]byte(event.ARI_Body), &c)
			fmt.Printf("Received digit %s\n", c.Digit)
			switch c.Digit {
			case "1":
			case "2":
				return changeFoldersIntro, vmMainState
			case "3":
				return advancedOptionsIntro, vmMainState
			case "0":
			case "*":
				pb, _ := a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-opts")
				vmMainState.AddPlayback(pb.Id)
			case "#":
				return hangupVMMain, vmMainState
			}
		case "PlaybackFinished":
			var p ari.PlaybackFinished
			json.Unmarshal([]byte(event.ARI_Body), &p)
			vmMainState.RemovePlayback(p.Playback.Id)
			return vmMainMenu, vmMainState

		}
	}
	return vmMainMenu, vmMainState
}

func advancedOptionsIntro(a *ari.AppInstance, vmMainState *vmMainInternal) (vmMainStateFunc, *vmMainInternal) {
	pb, _ := a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-tomakecall")
	vmMainState.AddPlayback(pb.Id)
	pb, _ = a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-leavemsg")
	vmMainState.AddPlayback(pb.Id)
	return advancedOptions, vmMainState
}

func advancedOptions(a *ari.AppInstance, vmMainState *vmMainInternal) (vmMainStateFunc, *vmMainInternal) {
	select {
	case event := <-a.Events:
		switch event.Type {
		case "ChannelDtmfReceived":
			vmMainState.PlaybacksStop(a)
			var c ari.ChannelDtmfReceived
			json.Unmarshal([]byte(event.ARI_Body), &c)
			switch c.Digit {
			case "4":
				pb, _ := a.ChannelsPlay(vmMainState.ChannelID, "sound:invalid")
				vmMainState.AddPlayback(pb.Id)
			case "5":
				pb, _ := a.ChannelsPlay(vmMainState.ChannelID, "sound:invalid")
				vmMainState.AddPlayback(pb.Id)
			case "*":
				return vmMainMenuIntro, vmMainState
			}
		case "PlaybackFinished":
			var p ari.PlaybackFinished
			json.Unmarshal([]byte(event.ARI_Body), &p)
			vmMainState.RemovePlayback(p.Playback.Id)
		}
	}
	return advancedOptions, vmMainState
}

func changeFoldersIntro(a *ari.AppInstance, vmMainState *vmMainInternal) (vmMainStateFunc, *vmMainInternal) {
	pb, _ := a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-changeto")
	vmMainState.AddPlayback(pb.Id)

	pb, _ = a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-press")
	vmMainState.AddPlayback(pb.Id)
	pb, _ = a.ChannelsPlay(vmMainState.ChannelID, "sound:digits/0")
	vmMainState.AddPlayback(pb.Id)
	pb, _ = a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-for")
	vmMainState.AddPlayback(pb.Id)
	vmMainState.PlayFolder(a, "New")
	pb, _ = a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-messages")
	vmMainState.AddPlayback(pb.Id)

	pb, _ = a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-press")
	vmMainState.AddPlayback(pb.Id)
	pb, _ = a.ChannelsPlay(vmMainState.ChannelID, "sound:digits/1")
	vmMainState.AddPlayback(pb.Id)
	pb, _ = a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-for")
	vmMainState.AddPlayback(pb.Id)
	vmMainState.PlayFolder(a, "Old")
	pb, _ = a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-messages")
	vmMainState.AddPlayback(pb.Id)

	pb, _ = a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-press")
	vmMainState.AddPlayback(pb.Id)
	pb, _ = a.ChannelsPlay(vmMainState.ChannelID, "sound:digits/2")
	vmMainState.AddPlayback(pb.Id)
	pb, _ = a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-for")
	vmMainState.AddPlayback(pb.Id)
	vmMainState.PlayFolder(a, "Work")
	pb, _ = a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-messages")
	vmMainState.AddPlayback(pb.Id)
	return changeFolders, vmMainState
}

func changeFolders(a *ari.AppInstance, vmMainState *vmMainInternal) (vmMainStateFunc, *vmMainInternal) {
	select {
	case event := <-a.Events:
		switch event.Type {
		case "ChannelDtmfReceived":
			vmMainState.PlaybacksStop(a)
			var c ari.ChannelDtmfReceived
			json.Unmarshal([]byte(event.ARI_Body), &c)
			switch c.Digit {
			case "0":
				vmMainState.CurrentFolder = "New"
				vmMainState.PlayFolder(a, "New")
				pb, _ := a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-messages")
				vmMainState.AddPlayback(pb.Id)
				return vmMainMenuIntro, vmMainState
			case "1":
				vmMainState.CurrentFolder = "Old"
				vmMainState.PlayFolder(a, "Old")
				pb, _ := a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-messages")
				vmMainState.AddPlayback(pb.Id)
				return vmMainMenuIntro, vmMainState
			case "2":
				vmMainState.CurrentFolder = "Work"
				vmMainState.PlayFolder(a, "Work")
				pb, _ := a.ChannelsPlay(vmMainState.ChannelID, "sound:vm-messages")
				vmMainState.AddPlayback(pb.Id)
				return vmMainMenuIntro, vmMainState
			case "*":
				return changeFoldersIntro, vmMainState
			case "#":
				return vmMainMenuIntro, vmMainState
			}
		case "PlaybackFinished":
			var p ari.PlaybackFinished
			json.Unmarshal([]byte(event.ARI_Body), &p)
			vmMainState.RemovePlayback(p.Playback.Id)
		}
	}
	return changeFolders, vmMainState
}

func getMessageCount(mailbox, domain, folder string) int {
	var count int
	db.Ping()
	err := getMessCount.QueryRow(mailbox, domain, folder).Scan(&count)
	if err != nil {
		fmt.Println(err)
		return 0
	}
	return count
}

func authorizeUser(mailbox, domain, password string) bool {
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
