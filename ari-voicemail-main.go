package "main"

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

// startVMMainApp starts the primary voicemail application for retrieving messages.
func startVMMainApp(app string) {
	return
}