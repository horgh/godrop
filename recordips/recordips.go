// Package recordips makes a client watch for user connection notices (as
// operator).
//
// Record each IP to a file (if it is not present), along with the nick and
// date.
//
// My use case is to add connecting IPs to a firewall rule.
package recordips

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/horgh/godrop"
	"github.com/horgh/iptables-manage/cidrlist"
	"github.com/horgh/irc"
)

func init() {
	godrop.Hooks = append(godrop.Hooks, Hook)
}

// Hook fires when an IRC message of some kind occurs.
//
// We look for CLICONN notices and record the IP.
//
// The notices look like:
// :irc.example.com NOTICE * :*** Notice -- CLICONN will will example.com 192.168.1.2 opers will 192.168.1.2 0 will
//
// Note this is ircd-ratbox specific.
func Hook(c *godrop.Client, message irc.Message) {
	if message.Command != "NOTICE" {
		return
	}

	// 2 parameters. * and the full notice as a single parameter.
	if len(message.Params) != 2 {
		return
	}

	noticePieces := strings.Fields(message.Params[1])

	if len(noticePieces) < 8 {
		return
	}

	if noticePieces[3] != "CLICONN" {
		return
	}

	ipFile, exists := c.Config["record-ip-file"]
	if !exists {
		return
	}

	nick := noticePieces[4]
	ip := noticePieces[7]

	comment := fmt.Sprintf("IRC: %s", nick)

	if err := cidrlist.RecordIP(ipFile, ip, comment, time.Now()); err != nil {
		log.Printf("recordips: Unable to record IP: %s", err)
		return
	}

	log.Printf("recordips: Recorded IP: %s (%s)", ip, nick)
}
