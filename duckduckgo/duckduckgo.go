// Package duckduckgo provides the ability to query DuckDuckGo from IRC.
package duckduckgo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/horgh/godrop"
	"github.com/horgh/irc"
	"golang.org/x/net/html"
)

// SearchResult holds a single parsed out search result.
type SearchResult struct {
	URL  string
	Text string
}

// RelatedTopic holds an instant answer API related topic answer.
type RelatedTopic struct {
	Text string
}

// Answer holds an instant answer API result.
type Answer struct {
	Heading       string
	AbstractText  string
	Type          string
	Answer        string
	AnswerType    string
	Redirect      string
	RelatedTopics []RelatedTopic

	// APIURL is not part of the response but it's nice to have on hand.
	APIURL string
}

// Regular expressions to match on !triggers
var ddgTriggerRe = regexp.MustCompile(`(?i)^\\s*[!.](?:ddg|d|g|google)(\\s+.*|$)`)
var ddg1TriggerRe = regexp.MustCompile(`(?i)^\\s*[!.](?:ddg1|d1|g1)(\\s+.*|$)`)
var duckTriggerRe = regexp.MustCompile(`(?i)^\\s*[!.](?:duck)(\\s+.*|$)`)

// Timeout on HTTP requests.
var timeout = 15 * time.Second

// Debug mode toggle. If enabled we will save responses to debugFile
// and if that file exists use its content instead of making additional
// HTTP requests.
var debug = false
var debugFile = "/tmp/ddg.out"

// init registers us to receive IRC messages.
func init() {
	godrop.Hooks = append(godrop.Hooks, Hook)
}

// Hook fires when an IRC message of some kind occurs.
//
// This can let us know whether to do anything or not.
func Hook(c *godrop.Client, message irc.Message) {
	if message.Command != "PRIVMSG" {
		return
	}

	if matches := ddgTriggerRe.FindStringSubmatch(
		message.Params[1]); matches != nil {
		hookDDG(c, message.Params[0], matches[1])
		return
	}

	if matches := ddg1TriggerRe.FindStringSubmatch(
		message.Params[1]); matches != nil {
		hookDDG1(c, message.Params[0], matches[1])
		return
	}

	if matches := duckTriggerRe.FindStringSubmatch(
		message.Params[1]); matches != nil {
		hookDuck(c, message.Params[0], matches[1])
	}
}

// hookDDG handles !ddg
func hookDDG(c *godrop.Client, target string, args string) {
	query := strings.TrimSpace(args)
	if len(query) == 0 {
		_ = c.Message(target, "Usage: !ddg <query>")
		return
	}

	search(c, target, query, 4)
}

// hookDDG handles !ddg1
func hookDDG1(c *godrop.Client, target string, args string) {
	query := strings.TrimSpace(args)
	if len(query) == 0 {
		_ = c.Message(target, "Usage: !ddg1 <query>")
		return
	}

	search(c, target, query, 1)
}

// hookDuck handles !duck
//
// We look up an instant answer and respond to the target.
func hookDuck(c *godrop.Client, target string, args string) {
	query := strings.TrimSpace(args)
	if len(query) == 0 {
		_ = c.Message(target, "Usage: !duck <query>")
		return
	}

	answer, err := getInstantAnswer(query)
	if err != nil {
		_ = c.Message(target, fmt.Sprintf("Failure: %s", err))
		return
	}

	// Topic summary (type A)
	if answer.Type == "A" {
		if len(answer.AbstractText) == 0 {
			_ = c.Message(target, fmt.Sprintf("Missing summary! (%s)",
				answer.APIURL))
			return
		}

		_ = c.Message(target, answer.AbstractText)
		return
	}

	// Disambiguation (type D)
	if answer.Type == "D" {
		if len(answer.RelatedTopics) > 0 && len(answer.RelatedTopics[0].Text) > 0 {
			_ = c.Message(target, fmt.Sprintf("Did you mean: %s",
				answer.RelatedTopics[0].Text))
			return
		}

		_ = c.Message(target, fmt.Sprintf("No exact result found. (%s).",
			answer.APIURL))
		return
	}

	// Category (Type C). Lists related. e.g. list of Simpsons characters.
	if answer.Type == "C" {
		if len(answer.RelatedTopics) > 0 && len(answer.RelatedTopics[0].Text) > 0 {
			_ = c.Message(target, fmt.Sprintf("First result: %s",
				answer.RelatedTopics[0].Text))
			return
		}
		_ = c.Message(target, fmt.Sprintf("No category found (%s).",
			answer.APIURL))
		return

	}

	// Exclusive (Type E). Exclusive. e.g., !bang
	if answer.Type == "E" {
		if len(answer.Redirect) > 0 {
			_ = c.Message(target, fmt.Sprintf("Found: %s", answer.Redirect))
			return
		}

		if len(answer.Answer) > 0 {
			_ = c.Message(target, fmt.Sprintf("Answer: %s", answer.Answer))
			return
		}

		_ = c.Message(target, fmt.Sprintf(
			"Exclusive match, but no redirect or answer. (%s)", answer.APIURL))
		return
	}

	// Name (type N). Name.
	if answer.Type == "N" {
		_ = c.Message(target,
			fmt.Sprintf("Name result found but not supported (%s)", answer.APIURL))
		return
	}

	if answer.Type == "" {
		_ = c.Message(target, fmt.Sprintf("No results. (%s)", answer.APIURL))
		return
	}

	_ = c.Message(target, fmt.Sprintf("Unknown answer type (%s). (%s)",
		answer.Type, answer.APIURL))
}

// getInstantAnswer queries the DuckDuckGo instant answer API.
// See https://duckduckgo.com/api
//
// Interesting keys:
//
// For topic summaries:
//
// AbstractText: Topic summary with no HTML
// Heading:

// For instant answers:
//
// Answer
// AnswerType

// For definitions
//
// Definition
func getInstantAnswer(query string) (Answer, error) {
	// I want to set headers, so I need to build and make the request this way.

	values := url.Values{}
	values.Set("q", query)
	values.Set("format", "json")
	values.Set("pretty", "1")
	values.Set("no_redirect", "1")
	values.Set("no_html", "1")
	values.Set("t", "github.com/horgh/irc/duckduckgo")

	apiURL := "https://api.duckduckgo.com/?" + values.Encode()

	request, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return Answer{}, fmt.Errorf("preparing request: %s", err)
	}

	client := http.Client{Timeout: timeout}

	log.Printf("Making request... [%s] (URL %s)", query, apiURL)

	resp, err := client.Do(request)
	if err != nil {
		return Answer{}, fmt.Errorf("failed to perform HTTP request: %s", err)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		_ = resp.Body.Close()
		return Answer{}, fmt.Errorf("read failure: %s", err)
	}
	_ = resp.Body.Close()

	answer := Answer{}
	err = json.Unmarshal(body, &answer)
	if err != nil {
		log.Printf("Body: %s", body)
		return Answer{}, fmt.Errorf("unable to decode: %s", err)
	}

	answer.APIURL = apiURL

	return answer, nil
}

// search looks up search results and outputs them to the target.
func search(c *godrop.Client, target string, query string, result int) {
	body, err := getRawSearchResults(query)
	if err != nil {
		_ = c.Message(target, fmt.Sprintf("Query failure: %s", err))
		return
	}

	results, err := parseSearchResults(body)
	if err != nil {
		_ = c.Message(target, fmt.Sprintf("Failure parsing results: %s", err))
		return
	}

	if len(results) == 0 {
		_ = c.Message(target, "No results.")
		return
	}

	for i := 0; i < result && i < len(results); i++ {
		_ = c.Message(target, fmt.Sprintf("%s - %s", results[i].URL,
			results[i].Text))
	}
}

// getRawSearchResults retrieves the results as an HTML document.
//
// We make an HTTP request (unless in debug mode, and then we may not).
func getRawSearchResults(query string) ([]byte, error) {
	// In debug mode we use the saved response if it is present rather than
	// making a new HTTP request.
	if debug {
		_, err := os.Lstat(debugFile)
		if err == nil {
			body, err := ioutil.ReadFile(debugFile)
			if err != nil {
				return nil, fmt.Errorf("debug file exists but could not read: %s", err)
			}
			log.Printf("Debug mode. Read %s", debugFile)
			return body, nil
		}
	}

	// I want to set headers, so I need to build and make the request this way.

	values := url.Values{}
	values.Set("q", query)
	values.Set("kl", "us-en")

	request, err := http.NewRequest("POST", "https://duckduckgo.com/lite/",
		strings.NewReader(values.Encode()))
	if err != nil {
		return nil, fmt.Errorf("preparing request: %s", err)
	}

	userAgent := "Lynx/2.8.8dev.2 libwww-FM/2.14 SSL-MM/1.4.1"

	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := http.Client{Timeout: timeout}

	log.Printf("Making request... [%s]", query)

	resp, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to perform HTTP request: %s", err)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("read failure: %s", err)
	}
	_ = resp.Body.Close()

	// In debug mode we save the response to disk.
	// This is because I don't want to repeatedly hit the site when debugging
	// if I can help it.
	if debug {
		err = ioutil.WriteFile(debugFile, body, 0777)
		if err != nil {
			return nil, fmt.Errorf("write failure: %s", err)
		}
	}

	return body, nil
}

// parseSearchResults parses out research results.
func parseSearchResults(body []byte) ([]*SearchResult, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("document parsing error: %s", err)
	}

	results := traverseForSearchResults(doc)
	return results, nil
}

// traverseForSearchResults looks through the document for search results.
//
// At each node in the document, we check to see if we have a search
// result. We recursively descend, depth first.
func traverseForSearchResults(node *html.Node) []*SearchResult {
	results := []*SearchResult{}

	result := grabSearchResult(node)
	if result != nil {
		results = append(results, result)
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		childResults := traverseForSearchResults(child)
		results = append(results, childResults...)
	}

	return results
}

// grabSearchResult checks if there is a search result starting at the
// current node in the tree.
//
// The HTML of the tree we are looking for looks like this:
//
// <tr>
//   <td valign="top">3.&nbsp;</td>
//   <td>
//     <a rel="nofollow" href="http://www.test.com/" class='result-link'>Platform to Create Organizational Testing and Certifications</a>
//   </td>
// </tr>
func grabSearchResult(node *html.Node) *SearchResult {
	// <tr>

	if node.Type != html.ElementNode || node.Data != "tr" {
		return nil
	}

	if len(node.Attr) != 0 {
		return nil
	}

	//   <td valign="top">3.&nbsp;</td>

	td1 := node.FirstChild

	// In several cases we have "hidden" text nodes as we traverse. Skip
	// them.
	if td1.Type == html.TextNode {
		td1 = td1.NextSibling
	}

	if td1 == nil || td1.Type != html.ElementNode || td1.Data != "td" {
		return nil
	}

	if len(td1.Attr) != 1 || td1.Attr[0].Key != "valign" ||
		td1.Attr[0].Val != "top" {
		return nil
	}

	//   <td>

	td2 := td1.NextSibling
	if td2.Type == html.TextNode {
		td2 = td2.NextSibling
	}

	if td2 == nil || td2.Type != html.ElementNode || td2.Data != "td" {
		return nil
	}

	if len(td2.Attr) != 0 {
		return nil
	}

	//     <a rel="nofollow" href="http://www.test.com/" class='result-link'>Platform to Create Organizational Testing and Certifications</a>

	anchor := td2.FirstChild
	if anchor.Type == html.TextNode {
		anchor = anchor.NextSibling
	}

	if anchor == nil || anchor.Type != html.ElementNode || anchor.Data != "a" {
		return nil
	}

	if len(anchor.Attr) != 3 {
		return nil
	}

	if anchor.Attr[0].Key != "rel" || anchor.Attr[0].Val != "nofollow" {
		return nil
	}

	if anchor.Attr[1].Key != "href" {
		return nil
	}
	href := anchor.Attr[1].Val

	if anchor.Attr[2].Key != "class" || anchor.Attr[2].Val != "result-link" {
		return nil
	}

	// Text in the <a>

	text := grabText(anchor)

	re := regexp.MustCompile("\\s+")
	text = re.ReplaceAllString(text, " ")

	return &SearchResult{URL: href, Text: text}
}

// grabText recursively descends and takes the text from all text nodes
// starting from the given node.
func grabText(node *html.Node) string {
	if node == nil {
		return ""
	}

	if node.Type == html.TextNode {
		return node.Data
	}

	if node.Type != html.ElementNode {
		return ""
	}

	text := ""
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		text += grabText(child)
	}

	return text
}
