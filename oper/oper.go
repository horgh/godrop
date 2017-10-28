// Package oper makes the client become an operator.
package oper

import (
	"log"

	"github.com/horgh/godrop"
	"github.com/horgh/irc"
)

func init() {
	godrop.Hooks = append(godrop.Hooks, Hook)
}

// Hook fires when an IRC message of some kind occurs.
// This can let us know whether to do anything or not.
func Hook(c *godrop.Client, message irc.Message) {
	if message.Command == irc.ReplyWelcome {
		// Try to oper if we have both an oper name and password.
		operName, exists := c.Config["oper-name"]
		if !exists {
			return
		}
		operPass, exists := c.Config["oper-password"]
		if !exists {
			return
		}
		if len(operName) == 0 || len(operPass) == 0 {
			return
		}

		if err := c.Oper(operName, operPass); err != nil {
			log.Printf("Unable to send OPER: %s", err)
			return
		}

		log.Printf("Sent OPER")
		return
	}

	// Successful oper. Apply user modes.
	if message.Command == irc.ReplyYoureOper {
		if err := sendUmode(c); err != nil {
			log.Printf("Problem sending MODE: %s", err)
			return
		}
		return
	}
}

// sendUmode sends the oper umodes with the MODE command.
func sendUmode(c *godrop.Client) error {
	operUmodes, exists := c.Config["oper-umodes"]
	if !exists {
		return nil
	}

	if err := c.UserMode(c.GetNick(), operUmodes); err != nil {
		return err
	}

	log.Printf("Sent MODE")
	return nil
}
