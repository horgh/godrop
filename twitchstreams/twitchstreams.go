// Package twitchstreams provides a way to notify about users streaming on
// Twitch.
//
// There are two main features:
// - Poll a list of usernames and notify to channels when they start streaming.
// - A channel trigger to look up the streaming status of users.
//
// Setup:
// - Register an application on the Twitch developers site and get a Client ID.
// - Set the client ID in the configuration with key "twitchstreams-client-id"
//
// Configuration options:
// - twitchstreams-channels - A space separated list of channels to notify
//   about when one of your default users starts streaming
// - twitchstreams-client-id - Your application's client ID. Register it on the
//   developer site.
// - twitchstreams-users - Users to notify about when they start streaming.
//   Also the default list of users when you use the !twitch trigger without a
//   username.
package twitchstreams

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/horgh/godrop"
	"github.com/horgh/irc"
)

func init() {
	godrop.Hooks = append(godrop.Hooks, Hook)
}

var triggerRE = regexp.MustCompile(`(?i)^\s*[!.]twitch\s*(.*)`)

// Hook fires when an IRC message of some kind occurs.
func Hook(c *godrop.Client, m irc.Message) {
	pollStreams(c)

	if m.Command != "PRIVMSG" {
		return
	}

	target := m.Params[0]
	text := m.Params[1]

	if matches := triggerRE.FindStringSubmatch(text); matches != nil {
		args := ""
		if len(matches) > 1 {
			args = strings.ToLower(strings.TrimSpace(matches[1]))
		}

		triggerTwitch(c, target, args)
		return
	}
}

var usernameStreaming = map[string]bool{}
var lastPollTime time.Time
var durationBetweenPolls = 10 * time.Minute

func pollStreams(c *godrop.Client) {
	now := time.Now()
	if now.Sub(lastPollTime) < durationBetweenPolls {
		return
	}
	lastPollTime = now

	users := getDefaultUsers(c.Config)
	for _, username := range users {
		streams, err := getStreams(c.Config["twitchstreams-client-id"], username)
		if err != nil {
			log.Printf("error retrieving streams for %s: %s", username, err)
			return
		}

		if len(streams) == 0 {
			usernameStreaming[username] = false
			continue
		}

		// If this is the first time, don't notify. We just started. Partly this is
		// a workaround to not try to output while unregistered.
		if _, polledUserAlready := usernameStreaming[username]; !polledUserAlready {
			usernameStreaming[username] = true
			continue
		}

		if usernameStreaming[username] {
			continue
		}

		usernameStreaming[username] = true

		for _, ch := range strings.Fields(c.Config["twitchstreams-channels"]) {
			for _, stream := range streams {
				_ = c.Message(ch, fmt.Sprintf("%s is streaming: %s", username,
					stream.Title))
			}
		}
	}
}

func triggerTwitch(c *godrop.Client, target, args string) {
	if args != "" {
		outputStreams(c, target, strings.Fields(args))
		return
	}

	users := getDefaultUsers(c.Config)
	outputStreams(c, target, users)
}

func outputStreams(c *godrop.Client, target string, usernames []string) {
	for _, username := range usernames {
		streams, err := getStreams(c.Config["twitchstreams-client-id"], username)
		if err != nil {
			_ = c.Message(target, fmt.Sprintf("error retrieving streams for %s: %s",
				username, err))
			return
		}

		if len(streams) == 0 {
			_ = c.Message(target, fmt.Sprintf("%s is not streaming", username))
			continue
		}

		for _, stream := range streams {
			_ = c.Message(target, fmt.Sprintf("%s is streaming: %s", username,
				stream.Title))
		}
	}
}

func getDefaultUsers(config map[string]string) []string {
	var users []string

	for _, u := range strings.Fields(config["twitchstreams-users"]) {
		u = strings.ToLower(strings.TrimSpace(u))
		if u == "" {
			continue
		}

		have := false
		for _, u2 := range users {
			if u2 == u {
				have = true
			}
		}
		if !have {
			users = append(users, u)
		}
	}

	return users
}

// Stream describes an active stream
type Stream struct {
	Title string
}

func getStreams(clientID, username string) ([]Stream, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, fmt.Errorf("no client ID given")
	}

	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return nil, fmt.Errorf("no username given")
	}

	// It's possible to request the streams of multiple usernames at once, but
	// since we don't get the username back in the stream object, there's no way
	// to re-map them to the username. Well, without separately looking up the
	// usernames to know their user IDs. So we just do one request per username.

	vals := url.Values{}
	vals.Set("user_login", username)

	u := "https://api.twitch.tv/helix/streams?" + vals.Encode()

	resp, err := get(clientID, u)
	if err != nil {
		return nil, fmt.Errorf("error looking up streams: %s", err)
	}

	dataInterface, ok := resp["data"]
	if !ok {
		return nil, fmt.Errorf("response is missing data key")
	}
	data, ok := dataInterface.([]interface{})
	if !ok {
		return nil, fmt.Errorf("data is not the expected type")
	}

	var streams []Stream
	for _, si := range data {
		s, ok := si.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("stream object is not the expected type")
		}
		titleI, ok := s["title"]
		if !ok {
			return nil, fmt.Errorf("stream title is not present")
		}
		title, ok := titleI.(string)
		if !ok {
			return nil, fmt.Errorf("stream title is not a string")
		}
		streams = append(streams, Stream{title})
	}

	return streams, nil
}

var client *http.Client

func get(clientID, url string) (map[string]interface{}, error) {
	if clientID == "" || url == "" {
		return nil, fmt.Errorf("missing client ID or url")
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating HTTP request: %s", err)
	}

	req.Header.Set("Client-ID", clientID)

	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Second,
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error performing HTTP request: %s", err)
	}

	buf, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("error reading response body: %s", err)
	}

	if err := resp.Body.Close(); err != nil {
		return nil, fmt.Errorf("error closing response body: %s", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unsuccessful request: %s: %s", resp.Status, buf)
	}

	m := map[string]interface{}{}
	if err := json.Unmarshal(buf, &m); err != nil {
		return nil, fmt.Errorf("error unmarshaling response: %s", err)
	}

	return m, nil
}
