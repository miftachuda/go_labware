package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/kardianos/service"
	bolt "go.etcd.io/bbolt"
)

// --- Structs for XML Parsing ---

// AjaxResponse models the root of the XML responses from the server
type AjaxResponse struct {
	XMLName    xml.Name     `xml:"resp"`
	Eval       Eval         `xml:"eval"`
	UpdateComp UpdateComp   `xml:"updateComp"`
	ViewState  string       `xml:"viewState"`
	JSCall     []JSCall     `xml:"jsCall"`
}

// Eval models the <eval> tag, containing JavaScript expressions
type Eval struct {
	Expr string `xml:"expr"`
}

// UpdateComp models the <updateComp> tag, which contains updated HTML
type UpdateComp struct {
	HTML string `xml:"html"`
}

// JSCall models the <jsCall> tag
type JSCall struct {
	Comp string `xml:"comp,attr"`
}

// --- Structs for Data Processing ---

// Sample holds the data parsed from the final table
type Sample struct {
	Section string
	Date    time.Time
	Code    string
	Value   string
	Param   string
	Unit    string
}

// --- Client for Managing State ---

// LimsClient holds the session state (client, cookies, viewstates, etc.)
type LimsClient struct {
	httpClient  *http.Client
	jsession    string
	ecAURL      string
	viewState   string
	uid         string
	uri         string
	allIDs      []string // Corresponds to 'all' and 'all2'
	externIDs   []string // Corresponds to 'extern2'
	
	// Stored parameters from OpenTable
	buttonRunID string
	tableIDs    []string
	popupIDs    []string
	viewGUID    string
	scriptSrc   string
	reqURL	 string
	csrfToken   string
}

// NewLimsClient creates a new client with a cookie jar
func NewLimsClient() *LimsClient {
	jar, _ := cookiejar.New(nil)
	return &LimsClient{
		httpClient: &http.Client{
			Jar: jar,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Replicates 'redirect: "manual"' for the login form
				if strings.Contains(req.URL.Path, "login.htm") {
					return http.ErrUseLastResponse
				}
				return nil // Allow other redirects
			},
		},
	}
}

// --- Ported Functions as Client Methods ---

// One performs the initial GET to get the JSESSIONID
func (c *LimsClient) One() error {
	req, err := http.NewRequest("GET", "https://apps.pertamina.com/LIMS/login.htm", nil)
	if err != nil {
		return err
	}
	
	req.Header.Set("Host", "apps.pertamina.com")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:106.0) Gecko/20100101 Firefox/106.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	// ... (other headers) ...
	req.Header.Set("Connection", "keep-alive")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("One: bad status: %s", resp.Status)
	}

	// Find the JSESSIONID cookie. The JS code was brittle; this is more robust.
	for _, cookie := range c.httpClient.Jar.Cookies(req.URL) {
		if cookie.Name == "JSESSIONID" {
			c.jsession = cookie.String()
			break
		}
	}
	
	if c.jsession == "" {
		return fmt.Errorf("One: could not find JSESSIONID cookie")
	}
	// fmt.Println("Step 1 (One) OK, JSESSIONID:", c.jsession)
	return nil
}

// LoginForm performs the POST request to log in
func (c *LimsClient) LoginForm(username, password string) error {
	data := url.Values{}
	data.Set("loginForm:username", username)
	data.Set("loginForm:password", password)
	data.Set("loginForm:password_lwentransmitter", "4kZBuVwmDa4NXnaSw2RjXxM6Ruda5TaVmxG2jnZaeSiGIBbBZx//5AxCpM+3KKHhkoeAEFDe2ZELhYG6WpI/PRfBuahkPL6iRRlX5wBkCi4o37U5KOaJyADWcgvfHZIf1knogAUE2ySUqwFEWmA17YYnYCaaOjsl1AV887VeG6U=")
	data.Set("lw.viewguid", "ecid_c10d524c1815e8faa82623d4a4b17e67")
	data.Set("javax.faces.ViewState", "ecruiser.util.SerializedComponent$TreeStructure@307106c3")
	data.Set("loginForm", "true")

	reqURL := "https://apps.pertamina.com/LIMS/login.htm?ec_eid=onclick&ec_cid=loginForm%3AlogButton"
	req, err := http.NewRequest("POST", reqURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}

	req.Header.Set("Host", "apps.pertamina.com")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:106.0) Gecko/20100101 Firefox/106.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://apps.pertamina.com")
	req.Header.Set("Referer", "https://apps.pertamina.com/LIMS/login.htm")
	req.Header.Set("Cookie", fmt.Sprintf("%s ec_aurl=L0xJTVMvbG9naW4uaHRt;", c.jsession))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// bodyBytes, _ := io.ReadAll(resp.Body)
	// var respp = string(bodyBytes)
	// fmt.Println(respp)
	// // Expecting a 302 Redirect
	// if resp.StatusCode != http.StatusFound {
	// 	return fmt.Errorf("LoginForm: bad status: %s (expected 302)", resp.Status)
	// }

	// Update JSESSIONID from the response
	found := false
	for _, cookie := range c.httpClient.Jar.Cookies(req.URL) {
		if cookie.Name == "JSESSIONID" {
			c.jsession = cookie.String()
			found = true
		}
	}
	if !found {
		return fmt.Errorf("LoginForm: could not find new JSESSIONID after login")
	}

	// fmt.Println("Step 2 (Login) OK, New JSESSIONID:", c.jsession)
	return nil
}

// MainPage loads the main dashboard and parses it for state
func (c *LimsClient) MainPage() error {
	reqURL := "https://apps.pertamina.com/LIMS/index.htm?init_weblims=true&ec_eid=onclick&ec_cid=loginForm%3AlogButton"
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Host", "apps.pertamina.com")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:106.0) Gecko/20100101 Firefox/106.0")
	req.Header.Set("Referer", "https://apps.pertamina.com/LIMS/login.htm")
	req.Header.Set("Cookie", fmt.Sprintf("%s ec_aurl=L0xJTVMvZXJyb3IuaHRt; lims_dsNameCookie=LABWARE_PROD; queryStringCookie=ec_eid=onclick&ec_cid=loginForm%%3AlogButton", c.jsession))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("MainPage: bad status: %s", resp.Status)
	}

	// bodyBytes, _ := io.ReadAll(resp.Body)
	// var respp = string(bodyBytes)
	// fmt.Println(respp)
	
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return err
	}

    sel := doc.Find("input[name='javax.faces.ViewState']")
	// Extract ViewState
	vs, exists := sel.Attr("value")
	if !exists {
		return fmt.Errorf("MainPage: could not find javax.faces.ViewState")
	}
	c.viewState = vs

	// Extract UID
	uid, exists := doc.Find("input[name='mf:workFlowTabPane:sel']").Attr("value")
	if !exists {
		return fmt.Errorf("MainPage: could not find mf:workFlowTabPane:sel")
	}
	c.uid = uid

	// Extract menu IDs
	var allIDs []string
	doc.Find(".sub > .mc > .mct").Each(func(i int, s *goquery.Selection) {
		html, _ := s.Html()
		parts := strings.Split(html, " ")
		if len(parts) > 1 && parts[1] == "DI" {
			id, _ := s.Parent().Attr("id")
			idParts := strings.Split(id, ":")
			if len(idParts) > 1 {
				allIDs = append(allIDs, idParts[1])
			}
		}
	})
	c.allIDs = allIDs
	if len(c.allIDs) == 0 {
		return fmt.Errorf("MainPage: could not find any 'DI' menu items")
	}

	// Get ec_aurl cookie
	for _, cookie := range c.httpClient.Jar.Cookies(req.URL) {
		if cookie.Name == "ec_aurl" {
			c.ecAURL = cookie.String()
			break
		}
	}
	if c.ecAURL == "" {
		return fmt.Errorf("MainPage: could not find ec_aurl cookie")
	}
	scriptSrc, exists := doc.Find(`script[src*="ec_resp"]`).Attr("src")
	if !exists {
		return fmt.Errorf("MainPage: could not find script with ec_resp")
	}

	// The src is relative, make it absolute
	if strings.HasPrefix(scriptSrc, "/") {
		scriptSrc = "https://apps.pertamina.com" + scriptSrc
	}
	c.scriptSrc = scriptSrc
	c.reqURL = reqURL
	fmt.Println("Script URL:", scriptSrc)

	// fmt.Println("Step 3 (MainPage) OK")
	return nil
}

func (c *LimsClient) extractcrsf() error {
	scriptReq, err := http.NewRequest("GET", c.scriptSrc, nil)
	if err != nil {
		return err
	}

	scriptReq.Header.Set("User-Agent","Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:106.0) Gecko/20100101 Firefox/106.0")
	scriptReq.Header.Set("Referer", c.reqURL)

	scriptResp, err := c.httpClient.Do(scriptReq)
	if err != nil {
		return err
	}
	defer scriptResp.Body.Close()

	if scriptResp.StatusCode != http.StatusOK {
		return fmt.Errorf("script request failed: %s", scriptResp.Status)
	}

	scriptBody, err := io.ReadAll(scriptResp.Body)
	if err != nil {
		return err
	}

	scriptData := string(scriptBody)

	// DEBUG: see content
	fmt.Println("Script response:")
	fmt.Println(scriptData)
	re := regexp.MustCompile(`csrfToken\s*:\s*'([^']+)'`)
	match := re.FindStringSubmatch(scriptData)

	if len(match) > 1 {
		csrfToken := match[1]
		c.csrfToken = csrfToken
		fmt.Println("CSRF Token:", csrfToken)
	}
	return nil
}
// OpenQuery clicks the first 'DI' menu item to get the table URI
func (c *LimsClient) OpenQuery() error {
	// The original JS passes the whole 'all' array, which is a bug.
	// It should pass one ID. We'll use the first one found.
	if len(c.allIDs) == 0 {
		return fmt.Errorf("OpenQuery: no 'allIDs' found from previous step")
	}
	uriID := c.allIDs[0]

	data := url.Values{}
	data.Set("mf:search:label", "")
	data.Set("mf:search", "")
	data.Set("mf:workFlowTabPane:sel", c.uid)
	data.Set("mf:workFlowTabPane_clPane", c.uid)
	data.Set("mf:workFlowTabPane:_fc_", "")
	// data.Set("lw.viewguid", "ecid_c10d524c1815e8faa82623d4a4b17e67")
	data.Set("javax.faces.ViewState", c.viewState)
	data.Set("mf", "true")
	

	reqURL := fmt.Sprintf("https://apps.pertamina.com/LIMS/index.htm?ec_eid=onclick&ec_cid=mf%%3A%s&ec_ajax=true&ts=%d", uriID, time.Now().UnixMilli())
	req, err := http.NewRequest("POST", reqURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}

	req.Header.Set("Host", "apps.pertamina.com")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:106.0) Gecko/20100101 Firefox/106.0")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://apps.pertamina.com")
	req.Header.Set("csrfToken", c.csrfToken)
	req.Header.Set("Referer", "https://apps.pertamina.com/LIMS/index.htm?init_weblims=true&ec_eid=onclick&ec_cid=loginForm%3AlogButton")
	//req.Header.Set("Cookie", fmt.Sprintf("_ga=GA1.2.208247498.1757897243; _ga_N1WDB3WDLD=GS2.1.s1762499570$o10$g1$t1762501662$j60$l0$h0; ai_user=Ax3oB|2025-09-08T00:17:20.093Z; %s; %s; lims_dsNameCookie=LABWARE_PROD; queryStringCookie=ec_eid=onclick&ec_cid=loginForm%%3AlogButton", c.jsession, c.ecAURL))
	req.Header.Set("Cookie", fmt.Sprintf("lw_focus_=X2VjaWQxNzYwMjg5OmZpZWxkX3VpZF9EMDUxODg1N18=; %s; lims_dsNameCookie=LABWARE_PROD; queryStringCookie=logout=true&ec_eid=onclick&ec_cid=loginForm:logButton; ec_aurl=L0xJTVMvZXJyb3IuaHRt; ai_user=Ax3oB|2025-09-08T00:17:20.093Z; _ga=GA1.2.208247498.1757897243; _ga_N1WDB3WDLD=GS2.1.s1762499570$o10$g1$t1762501662$j60$l0$h0", c.jsession))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OpenQuery: bad status: %s", resp.Status)
	}

	
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed reading body: %v", err)
	}
	var ajaxResp AjaxResponse
	var resppsn =  string(bodyBytes);
	fmt.Println(resppsn);
	if err := xml.Unmarshal(bodyBytes, &ajaxResp); err != nil {
		return fmt.Errorf("OpenQuery: failed to parse XML: %v", err)
	}

	// Extract URI from <eval>'string'</eval>
	parts := strings.Split(ajaxResp.Eval.Expr, "'")
	if len(parts) < 4 {
		return fmt.Errorf("OpenQuery: could not parse URI from eval expr: %s", ajaxResp.Eval.Expr)
	}
	c.uri = parts[3]

	// fmt.Println("Step 4 (OpenQuery) OK, URI:", c.uri)
	return nil
}

// OpenTable loads the data table page and parses it for more state
func (c *LimsClient) OpenTable() error {
	reqURL := fmt.Sprintf("https://apps.pertamina.com/LIMS/%s", c.uri)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return err
	}
	
	req.Header.Set("Host", "apps.pertamina.com")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0")
	req.Header.Set("Referer", "https://apps.pertamina.com/LIMS/index.htm?init_weblims=true&ec_eid=onclick&ec_cid=loginForm%3AlogButton")
	req.Header.Set("Cookie", fmt.Sprintf("lims_dsNameCookie=LabWareV6Prod; queryStringCookie=ec_eid=onclick&ec_cid=loginForm%%3AlogButton; %s lw_focus_=bWY6c2VhcmNo", c.jsession))
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OpenTable: bad status: %s", resp.Status)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return err
	}

	// Find "Run" button ID
	foundRun := false
	doc.Find("img").Each(func(i int, s *goquery.Selection) {
		if title, _ := s.Attr("title"); title == "Run" {
			id, _ := s.Parent().Attr("id")
			parts := strings.Split(id, ":")
			if len(parts) > 2 {
				c.buttonRunID = parts[2]
				foundRun = true
			}
		}
	})
	if !foundRun {
		return fmt.Errorf("OpenTable: could not find 'Run' button")
	}

	// Find table IDs
	doc.Find(".dataTableMain").Each(func(i int, s *goquery.Selection) {
		id, _ := s.Attr("id")
		c.tableIDs = append(c.tableIDs, strings.Split(id, ":mainRow")[0])
	})
	if len(c.tableIDs) == 0 {
		return fmt.Errorf("OpenTable: could not find '.dataTableMain'")
	}

	// Find popup IDs
	doc.Find(".popup").Each(func(i int, s *goquery.Selection) {
		id, _ := s.Attr("id")
		c.popupIDs = append(c.popupIDs, id)
	})
	if len(c.popupIDs) == 0 {
		return fmt.Errorf("OpenTable: could not find '.popup'")
	}

	vs, exists := doc.Find("input[name='javax.faces.ViewState']").Attr("value")
	if !exists {
		return fmt.Errorf("OpenTable: could not find 'javax.faces.ViewState'")
	}
	c.viewState = vs

	// Find 'all2' and 'extern2' IDs
	var all2, extern2 []string
	doc.Find(".mct").Each(func(i int, s *goquery.Selection) {
		html, _ := s.Html()
		parts := strings.Split(html, " ")
		if len(parts) > 1 {
			id, _ := s.Parent().Attr("id")
			idParts := strings.Split(id, ":")
			if len(idParts) > 2 {
				switch parts[1] {
				case "DI":
					all2 = append(all2, idParts[2])
				case "EXT":
					extern2 = append(extern2, idParts[2])
				}
			}
		}
	})
	c.allIDs = all2
	c.externIDs = extern2

	// fmt.Println("Step 5 (OpenTable) OK")
	return nil
}

func (c *LimsClient) OpenDate() (string, *goquery.Document, string, error) {
	data := url.Values{}
	data.Set("mf:search:label", "")
	data.Set("mf:search", "")
	data.Set(fmt.Sprintf("%s:editcache", c.tableIDs[0]), "")
	// ... other form data fields from JS ...
	data.Set("javax.faces.ViewState", c.viewState)
	data.Set("mf", "true")

	reqURL := fmt.Sprintf("https://apps.pertamina.com/LIMS/%s?ec_eid=onclick&ec_cid=mf%%3Atp%%3A%s&ec_ajax=true&ts=%d", c.uri, c.buttonRunID, time.Now().UnixMilli())
	req, err := http.NewRequest("POST", reqURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", nil, "", err
	}
	
	req.Header.Set("Host", "apps.pertamina.com")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:106.0) Gecko/20100101 Firefox/106.0")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("csrfToken", c.csrfToken)
	req.Header.Set("Origin", "https://apps.pertamina.com")
	req.Header.Set("Referer", fmt.Sprintf("https://apps.pertamina.com/LIMS/%s", c.uri))
	req.Header.Set("Cookie", fmt.Sprintf("%s lims_dsNameCookie=LABWARE_PROD; queryStringCookie=ec_eid=onclick&ec_cid=loginForm%%3AlogButton; ec_aurl=L0xJTVMvZXJyb3IuaHRt;", c.jsession))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", nil, "", err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	var ajaxResp AjaxResponse
	if err := xml.Unmarshal(bodyBytes, &ajaxResp); err != nil {
		return "", nil, "", fmt.Errorf("OpenDate: failed to parse XML: %v", err)
	}

	// Parse the HTML from the XML response to find the button
	html := ajaxResp.UpdateComp.HTML
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return "", nil, "", fmt.Errorf("OpenDate: failed to parse popup HTML: %v", err)
	}
	
	buttonID, exists := doc.Find("button").First().Attr("id")
	if !exists {
		return "", nil, "", fmt.Errorf("OpenDate: could not find button ID in popup")
	}

	// Parse the ViewState from the XML response
	vsHTML := ajaxResp.ViewState
	docVS, err := goquery.NewDocumentFromReader(strings.NewReader(vsHTML))
	if err != nil {
		return "", nil, "", fmt.Errorf("OpenDate: failed to parse ViewState HTML: %v", err)
	}
	var viewState string

	viewState, exists = docVS.Find("#javax.faces.ViewState").Attr("value")
	if !exists {
		// Fallback: sometimes the input has name attribute
		viewState, exists = docVS.Find("input[name='javax.faces.ViewState']").Attr("value")
		if !exists {
			return "", nil, "", fmt.Errorf("OpenDate: could not find new ViewState")
		}
	}


	// fmt.Println("Step 6 (OpenDate) OK")
	return buttonID, doc, viewState, nil
}

// ClickOK simulates clicking "OK" on the date popup
// Returns the final table DOM, new viewstate, and other params for OnHide
func (c *LimsClient) ClickOK(buttonID string, dom *goquery.Document, viewState string) (*goquery.Document, string, string, string, string, error) {
	date2 := time.Now().Format("2006-01-02")
	
	var docIDs []string
	var inputs []*goquery.Selection
	dom.Find("input").Each(func(i int, s *goquery.Selection) {
		id, _ := s.Attr("id")
		docIDs = append(docIDs, id)
		inputs = append(inputs, s)
	})

	if len(inputs) < 11 {
		return nil, "", "", "", "", fmt.Errorf("ClickOK: not enough input fields in popup DOM")
	}

	lwview, _ := inputs[10].Attr("value")
	weblist, _ := inputs[0].Attr("popup")
	
	ecnumStr := docIDs[0][11:14]
	ecnum, _ := strconv.Atoi(ecnumStr)
	ec := docIDs[0][0:11]
	
	inp, _ := dom.Find(".iconinput-input").Eq(1).Attr("value")

	obj := url.Values{}
	obj.Set(fmt.Sprintf("%s:popupState", docIDs[6][0:len(docIDs[6])-11]), "popup")
	obj.Set(fmt.Sprintf("%s:order", docIDs[6][0:len(docIDs[6])-11]), "3")
	obj.Set("lw.viewguid", lwview)
	obj.Set("javax.faces.ViewState", "") // ViewState is empty in the JS object
	obj.Set(docIDs[0][0:strings.Index(docIDs[0], ":")], "true")
	obj.Set(docIDs[0][0:len(docIDs[0])-4], "")
	obj.Set(fmt.Sprintf("%s:value", docIDs[0][0:len(docIDs[0])-4]), "")
	obj.Set(docIDs[2][0:len(docIDs[2])-2], inp)
	obj.Set(fmt.Sprintf("%s:%s%d:_fc_", docIDs[0][0:strings.Index(docIDs[0], ":")], ec, ecnum+2), "")
	obj.Set(fmt.Sprintf("%s:%s%d", docIDs[0][0:strings.Index(docIDs[0], ":")], ec, ecnum+2), date2)
	obj.Set(fmt.Sprintf("%s:%s%d:hasContent", docIDs[0][0:strings.Index(docIDs[0], ":")], ec, ecnum+1), "true")
	obj.Set(docIDs[4][0:len(docIDs[4])-2], inp)
	obj.Set(fmt.Sprintf("%s:%s%d:_fc_", docIDs[0][0:strings.Index(docIDs[0], ":")], ec, ecnum+4), "")
	obj.Set(fmt.Sprintf("%s:%s%d", docIDs[0][0:strings.Index(docIDs[0], ":")], ec, ecnum+4), date2)
	obj.Set(fmt.Sprintf("%s:%s%d:hasContent", docIDs[0][0:strings.Index(docIDs[0], ":")], ec, ecnum+3), "true")
	obj.Set(fmt.Sprintf("%s:%s:hasContent", docIDs[0][0:strings.Index(docIDs[0], ":")], weblist), "true")
	obj.Set(fmt.Sprintf("%s:windowState", docIDs[6][0:len(docIDs[6])-11]), "window:normal;")
	obj.Set(fmt.Sprintf("%s:hasContent", docIDs[6][0:len(docIDs[6])-11]), "true")
	obj.Set(fmt.Sprintf("%s:windowSize", docIDs[6][0:len(docIDs[6])-11]), "420@209")
	
	// Override javax.faces.ViewState with the one passed in
	obj.Set("javax.faces.ViewState", viewState)

	data := obj.Encode()

	lwfocus := base64.StdEncoding.EncodeToString([]byte(docIDs[0][0 : len(docIDs[0])-4]))
	ecfocus := base64.StdEncoding.EncodeToString([]byte(buttonID))
	butArr := strings.Split(buttonID, ":")
	
	reqURL := fmt.Sprintf("https://apps.pertamina.com/LIMS/%s?ec_eid=onclick&ec_cid=%s%%3A%s&ec_ajax=true&ts=%d", c.uri, butArr[0], butArr[1], time.Now().UnixMilli())
	req, err := http.NewRequest("POST", reqURL, strings.NewReader(data))
	if err != nil {
		return nil, "", "", "", "", err
	}

	req.Header.Set("Host", "apps.pertamina.com")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/107.0.0.0 Safari/537.36")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("csrfToken", c.csrfToken)
	req.Header.Set("Referer", fmt.Sprintf("https://apps.pertamina.com/LIMS/%s", c.uri))
	req.Header.Set("Cookie", fmt.Sprintf("%s ec_aurl=L1dlYkxJTVMvZXJyb3IuaHRt; lims_dsNameCookie=LabWareV6Prod; queryStringCookie=ec_eid=onclick&ec_cid=loginForm%%3AlogButton; lw_focus_=%s; ec_focus=%s", c.jsession, lwfocus, ecfocus))
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", "", "", "", err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	var ajaxResp AjaxResponse
	if err := xml.Unmarshal(bodyBytes, &ajaxResp); err != nil {
		return nil, "", "", "", "", fmt.Errorf("ClickOK: failed to parse XML: %v", err)
	}

	// Parse final table HTML
	html := ajaxResp.UpdateComp.HTML
	domTableFinal, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, "", "", "", "", fmt.Errorf("ClickOK: failed to parse final table HTML: %v", err)
	}
	
	// Parse new ViewState
	vsHTML := ajaxResp.ViewState
	docVS, err := goquery.NewDocumentFromReader(strings.NewReader(vsHTML))
	if err != nil {
		return nil, "", "", "", "", fmt.Errorf("ClickOK: failed to parse new ViewState HTML: %v", err)
	}
	newViewState, _ := docVS.Find("#javax.faces.ViewState").Attr("value")
	
	onHidelink := ""
	if len(ajaxResp.JSCall) > 0 {
		onHidelink = ajaxResp.JSCall[0].Comp
	}
	
	// fmt.Println("Step 7 (ClickOK) OK")
	return domTableFinal, newViewState, lwfocus, ecfocus, onHidelink, nil
}

// OnHide sends the 'onhide' event
func (c *LimsClient) OnHide(viewState, lwfocus, ecfocus, onHidelink string) error {
	if onHidelink == "" {
		// fmt.Println("Step 8 (OnHide) SKIPPED (no onHidelink)")
		return nil // Not always present
	}
	
	params := url.Values{}
	params.Set("ec_eid", "onhide")
	params.Set("ec_cid", onHidelink)
	params.Set("ec_ajax", "true")
	params.Set("ts", fmt.Sprintf("%d", time.Now().UnixMilli()))
	
	reqURL := fmt.Sprintf("https://apps.pertamina.com/LIMS/%s?%s", c.uri, params.Encode())
	// This is a POST with an empty body but params in URL
	req, err := http.NewRequest("POST", reqURL, nil) 
	if err != nil {
		return err
	}
	
	req.Header.Set("Host", "apps.pertamina.com")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/108.0.0.0 Safari/537.36")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", fmt.Sprintf("https://apps.pertamina.com/LIMS/%s", c.uri))
	req.Header.Set("Cookie", fmt.Sprintf("%s lims_dsNameCookie=LabWareV6Prod; queryStringCookie=ec_eid=onclick&ec_cid=loginForm%%3AlogButton; ec_aurl=L1dlYkxJTVMvZXJyb3IuaHRt; lw_focus_=%s ec_focus=%s", c.jsession, lwfocus, ecfocus))
	req.Header.Set("Content-Length", "0") // Empty body

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	var ajaxResp AjaxResponse
	if err := xml.Unmarshal(bodyBytes, &ajaxResp); err != nil {
		return fmt.Errorf("OnHide: failed to parse XML: %v", err)
	}

	// Parse and update the final ViewState
	vsHTML := ajaxResp.ViewState
	docVS, err := goquery.NewDocumentFromReader(strings.NewReader(vsHTML))
	if err != nil {
		return fmt.Errorf("OnHide: failed to parse ViewState HTML: %v", err)
	}
	newViewState, _ := docVS.Find("#javax.faces.ViewState").Attr("value")
	c.viewState = newViewState
	
	// fmt.Println("Step 8 (OnHide) OK")
	return nil
}

// RefreshTable performs the refresh action
// NOTE: This function uses the corrected parameters
func (c *LimsClient) RefreshTable() error {
	if len(c.externIDs) == 0 {
		return fmt.Errorf("RefreshTable: no externIDs found (from OpenTable)")
	}
	// The JS bug used the whole array; we'll use the first ID
	switchButton := c.externIDs[0] 

	data := url.Values{}
	data.Set("mf:search:label", "")
	data.Set("mf:search", "")
	data.Set(fmt.Sprintf("%s:editcache", c.tableIDs[0]), "")
	data.Set(fmt.Sprintf("%s:1:3:TbCheckBox", c.tableIDs[0]), "true")
	data.Set(fmt.Sprintf("%s:2:3:TbCheckBox", c.tableIDs[0]), "true")
	// ... (other fields)
	if len(c.tableIDs) > 1 {
		data.Set(fmt.Sprintf("%s:_fc_", c.tableIDs[1]), "")
		data.Set(fmt.Sprintf("%s:columnWidths", c.tableIDs[1]), "")
	}
	if len(c.popupIDs) > 0 {
		data.Set(fmt.Sprintf("%s:hasContent", c.popupIDs[0]), "true")
	}
	if len(c.popupIDs) > 1 {
		data.Set(fmt.Sprintf("%s:hasContent", c.popupIDs[1]), "true")
	}
	data.Set("lw.viewguid", c.viewGUID)
	data.Set("javax.faces.ViewState", c.viewState) // Use the latest viewstate
	data.Set("mf", "true")
	
	reqURL := fmt.Sprintf("https://apps.pertamina.com/LIMS/%s?ec_eid=onclick&ec_cid=mf%%3Atp%%3A%s&ec_ajax=true&ts=%d", c.uri, switchButton, time.Now().UnixMilli())
	req, err := http.NewRequest("POST", reqURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	
	req.Header.Set("Host", "apps.pertamina.com")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:106.0) Gecko/20100101 Firefox/106.0")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", fmt.Sprintf("https://apps.pertamina.com/LIMS/%s", c.uri))
	req.Header.Set("Cookie", fmt.Sprintf("%s lims_dsNameCookie=LabWareV6Prod; queryStringCookie=ec_eid=onclick&ec_cid=loginForm%%3AlogButton; ec_aurl=L1dlYkxJTVMvZXJyb3IuaHRt; ", c.jsession))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("RefreshTable: bad status: %s", resp.Status)
	}

	// This request updates state on the server, no data processing needed here
	// fmt.Println("Step 9 (RefreshTable) OK")
	return nil
}

// --- Data Processing Functions ---

// sortSample sorts a slice of Sample structs into three shifts
func sortSample(rawSam []Sample) ([][]Sample, error) {
	var sampleMalem, samplePagi, sampleSore []Sample
	
	for _, sample := range rawSam {
		hour := sample.Date.Hour()
		if hour < 8 {
			sampleMalem = append(sampleMalem, sample)
		} else if hour >= 8 && hour < 16 {
			samplePagi = append(samplePagi, sample)
		} else { // 16:00 - 23:59
			sampleSore = append(sampleSore, sample)
		}
	}
	return [][]Sample{sampleMalem, samplePagi, sampleSore}, nil
}

// detectShift detects the shift name from a sample group
func detectShift(sampleGroup []Sample) string {
	if len(sampleGroup) == 0 {
		return "No Sample"
	}
	hour := sampleGroup[0].Date.Hour()
	if hour < 8 {
		return "Malam"
	} else if hour >= 8 && hour < 16 {
		return "Pagi"
	}
	return "Sore"
}


type PointValue struct {
	Value string `json:"value"`
	Unit  string `json:"unit"`
}
func castSampleJSON(sam []Sample) (string, error) {
	shift := detectShift(sam)

	// This is the main "samples" map.
	// It will map a "code" (e.g., "02405") to its map of points.
	samplesMap := make(map[string]map[string]PointValue)

	// Loop through every single sample reading
	for _, s := range sam {

		// 1. Check if we've seen this code (e.g., "02405") before.
		// If not, create a new inner map for it.
		if _, ok := samplesMap[s.Code]; !ok {
			samplesMap[s.Code] = make(map[string]PointValue)
		}

		// 2. Add the point to the inner map.
		// The "param" (e.g., "Color") is now the KEY.
		samplesMap[s.Code][s.Param] = PointValue{
			Value: s.Value, // Value is kept as a string
			Unit:  s.Unit,
		}
	}

	// Build the final result object
	result := map[string]interface{}{
		"shift":   shift,
		"samples": samplesMap, // Assign the whole map we just built
	}

	// Encode to JSON
	// Using MarshalIndent for nice formatting, use json.Marshal for compact output
	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to encode JSON: %v", err)
	}

	return string(jsonBytes), nil
}

var replacer = strings.NewReplacer(
	"Kinematic Viscosity at 40°C", "Visco 40°C",
	"Kinematic Viscosity at 60°C", "Visco 60°C",
	"Kinematic Viscosity at 100°C", "Visco 100°C",
	"Flash Point COC", "FP COC",
	"Flash Point PMCC", "FP PMCC",
	"Refractive Index 70°C", "RI 70°C",
	"Refractive Index 20°C", "RI 20°C",
	"Appearance", "App",
	"Specific Gravity at 70°C", "Sg 70°C",
	"Colour ASTM", "Color",
	"Conradson Carbon Residue", "CCR",
	"Specific Gravity at 60/60°F", "Sg 60/60°F",
	"Viscosity Gravity Constant", "VGC",
	"Pour Point", "PP",
	"Viscosity Index", "VI",
	"Traces Methyl Ethyl Ketone", "Traces MEK",
	"MM2_S", "cSt",
	"NONE", "",
	"DEG_C", "°C",
	"ASTM_UNIT", "",
	"DEG_F", "°F",
	"PCT_MM", "",
	"Clear &amp; Bright", "C&B", // Handle HTML entity
	"Clear & Bright", "C&B",
)

func stringRep(text string) string {
	return replacer.Replace(text)
}

// proceesArray parses the final HTML table DOM into Sample structs
func processArray(arrayIn *goquery.Selection) ([][]Sample, error) {
	var samples []Sample

	var rows *goquery.Selection

	// Check if the passed-in selection is ALREADY the tbody
	if arrayIn.Is("tbody") {
		rows = arrayIn.Children()
	} else {
		// Otherwise, assume it's a parent (like <table>) and find the tbody
		rows = arrayIn.Find("tbody").Children()
	}

	rows.Each(func(i int, row *goquery.Selection) {
		cols := row.Children()

		// --- THIS IS THE FIX ---
		// Check if the row has enough columns (we need up to index 8)
		if cols.Length() < 9 {
			return // Skips this row (like 'continue' in a normal loop)
		}

		// Now, check if the date column (a key field) is empty.
		// If it's empty, it's not a valid data row, so we skip it.
		dateStr := strings.TrimSpace(cols.Eq(1).Text())
		if dateStr == "" {
			return // Skips this row
		}
		// --- END FIX ---

		// Date parsing: "11/05/2025 12:00:51 AM"
		layout := "2006-01-02 15:04:05"
		layout1 := "01/02/2006 03:04:05 PM"
		layout2 := "02-Jan-2006"
		date, err := time.Parse(layout, dateStr)
		if err != nil {
			date, err = time.Parse(layout1, dateStr)
			if err != nil {
				date, err = time.Parse(layout2, dateStr)
				if err != nil {
					date = time.Now()
				}
			}
		}

		samples = append(samples, Sample{
			Section: strings.TrimSpace(cols.Eq(2).Text()),
			Date:    date,
			Code:    strings.TrimSpace(cols.Eq(4).Text()),
			Value:   strings.TrimSpace(cols.Eq(8).Text()),
			Param:   strings.TrimSpace(cols.Eq(6).Text()),
			Unit:    strings.TrimSpace(cols.Eq(7).Text()),
		})
	})

	if len(samples) == 0 {
		fmt.Println("Warning: processArray found no samples in the table.")
		// Return empty shifts to avoid panic
		return [][]Sample{{}, {}, {}}, nil
	}

	sorted, err := sortSample(samples)
	if err != nil {
		return nil, err
	}
	
	return sorted, nil
}


func GetData() ([3]string, error) {
	password := "XXXXXXXXXXX"
	
	client := NewLimsClient()

	fmt.Println("starting process...")
	if err := client.One(); err != nil {
		return [3]string{}, fmt.Errorf("step 1 (one) failed: %v", err)
	}
	
	if err := client.LoginForm("sutanto", password); err != nil {
		return [3]string{}, fmt.Errorf("step 2 (loginform) failed: %v", err)
	}
	
	if err := client.MainPage(); err != nil {
		return [3]string{}, fmt.Errorf("step 3 (mainpage) failed: %v", err)
	}
	if err := client.extractcrsf(); err != nil {
		return [3]string{}, fmt.Errorf("step 3.5 (extractcsrf) failed: %v", err)
	}
	
	
	if err := client.OpenQuery(); err != nil {
		return [3]string{}, fmt.Errorf("step 4 (openquery) failed: %v", err)
	}
	
	if err := client.OpenTable(); err != nil {
		return [3]string{}, fmt.Errorf("step 5 (opentable) failed: %v", err)
	}
	
	// Run 1 (for dom_loc2)
	buttonID1, dom1, viewState1, err := client.OpenDate()
	if err != nil {
		return [3]string{}, fmt.Errorf("step 6 (opendate 1) failed: %v", err)
	}
	
	domLoc2, vs5, lwfocus, ecfocus, onHidelink, err := client.ClickOK(buttonID1, dom1, viewState1)
	if err != nil {
		return [3]string{}, fmt.Errorf("step 7 (clickok 1) failed: %v", err)
	}
	
	if err := client.OnHide(vs5, lwfocus, ecfocus, onHidelink); err != nil {
		return [3]string{}, fmt.Errorf("step 8 (onhide) failed: %v", err)
	}
	
	// This step was bugged in the JS. Using corrected parameters.
	if err := client.RefreshTable(); err != nil {
		return [3]string{}, fmt.Errorf("step 9 (refreshtable) failed: %v", err)
	}

	// Run 2 (for dom_ext)
	buttonID2, dom2, viewState2, err := client.OpenDate()
	if err != nil {
		return [3]string{}, fmt.Errorf("step 10 (opendate 2) failed: %v", err)
	}
	
	_, _, _, _, _, err = client.ClickOK(buttonID2, dom2, viewState2)
	if err != nil {
		return [3]string{}, fmt.Errorf("step 11 (clickok 2) failed: %v", err)
	}
	
	fmt.Println("processing data...")
	tableRoot := domLoc2.Find(".dataTableInner").First()
	if tableRoot.Length() == 0 {
		return [3]string{}, fmt.Errorf("step 12 (process): could not find .dataTableInner in final dom")
	}

	dataLoc2, err := processArray(tableRoot)
	if err != nil {
		return [3]string{}, fmt.Errorf("step 12 (process): processArray failed: %v", err)
	}

	castedLoc2Malam, _ := castSampleJSON(dataLoc2[0])
	castedLoc2Pagi, _ := castSampleJSON(dataLoc2[1])
	castedLoc2Sore, _ := castSampleJSON(dataLoc2[2])

	finalResultLoc2M := stringRep(castedLoc2Malam)
	finalResultLoc2P := stringRep(castedLoc2Pagi)
	finalResultLoc2S := stringRep(castedLoc2Sore)

	fmt.Println("Process finished successfully.")
	return [3]string{finalResultLoc2M, finalResultLoc2P, finalResultLoc2S}, nil
}
type SampleData struct {
	Samples map[string]interface{} `json:"samples"`
	Shift   string                 `json:"shift"`
}
type CachedData struct {
	Samples []Sample `json:"samples"`
	// add other fields if needed
}

type ShiftCacheItem struct {
	Data      string    `json:"data"`
	Timestamp time.Time `json:"timestamp"`
}

var (
	dbFile        = "shift_cache.db"
	db            *bolt.DB
	mu            sync.Mutex
)

// initialize BoltDB
func initDB() error {
	var err error
	db, err = bolt.Open(dbFile, 0600, nil)
	if err != nil {
		return err
	}
	// Ensure bucket exists
	return db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("shifts"))
		return err
	})
}

// close database
func closeDB() {
	if db != nil {
		db.Close()
	}
}

// utility
func isSamplesEmpty(jsonStr string) bool {
	var data SampleData
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return true
	}
	return len(data.Samples) == 0
}

// key format: shiftIndex + date (e.g. "0_2025-11-05")
func makeKey(index int, date time.Time) string {
	return fmt.Sprintf("%d_%s", index, date.Format("2006-01-02"))
}

func saveSampleData(index int, data string) error {
	item := ShiftCacheItem{
		Data:      data,
		Timestamp: time.Now(),
	}

	encoded, err := json.Marshal(item)
	if err != nil {
		return err
	}

	key := makeKey(index, time.Now())

	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("shifts"))
		return b.Put([]byte(key), encoded)
	})
}

func loadSampleData(index int) (string, bool, error) {
	var item ShiftCacheItem
	now := time.Now()
	todayKey := makeKey(index, now)

	found := false
	var raw []byte

	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("shifts"))
		raw = b.Get([]byte(todayKey))
		if raw == nil {
			return nil // don't load yesterday here anymore
		}
		found = true
		return json.Unmarshal(raw, &item)
	})
	if err != nil {
		return "", false, err
	}

	if !found || isSamplesEmpty(item.Data) {
		return "", false, nil
	}
	return item.Data, true, nil
}

func getCachedData(index int) (string, error) {
	mu.Lock()
	defer mu.Unlock()

	// try loading today's cache only
	cachedStr, found, err := loadSampleData(index)
	if err != nil {
		return "", err
	}

	if found {
		var cachedMap map[string]interface{}
		if err := json.Unmarshal([]byte(cachedStr), &cachedMap); err != nil {
			fmt.Println("⚠️ Failed to parse cached JSON:", err)
		} else if samplesMap, ok := cachedMap["samples"].(map[string]interface{}); ok {
			if len(samplesMap) > 5 {
				fmt.Println("✅ Loaded from today's cache (enough samples)")
				return cachedStr, nil
			}
			fmt.Println("⚠️ Cache found but too few samples:", len(samplesMap))
		} else {
			fmt.Println("⚠️ Invalid cache format (no 'samples' field)")
		}
	}

	// cache not found or insufficient → fetch new data
	fmt.Println("🔄 Fetching new data...")
	newData, err := GetData()
	if err != nil {
		return "", err
	}

	for i := 0; i < 3; i++ {
		if !isSamplesEmpty(newData[i]) {
			if err := saveSampleData(i, newData[i]); err != nil {
				fmt.Println("⚠️ Failed to save cache:", err)
			}
		}
	}

	return newData[index], nil
}
var sampleNames = map[string]string{
		"02101": "Long Residue",
    "02102": "VGO",
    "02103": "SPO",
    "02104": "LMO",
    "02105": "MMO",
    "02107": "Short Residue",
    "02201": "Short Residue",
    "02202": "Asphalt",
    "02203": "DAO",
    "02301": "Distillate",
    "02302": "Raffinate",
    "023C108": "Raffinate C108",
    "02303": "Extract",
    "02304": "Feed 023C-105",
    "02310": "Water to Drain",
    "02401": "HDT",
    "02405": "DOR",
    "02406": "SLack Wax",
	"02407": "Dry Solvent",
	"02408": "Wet Solvent",
    "02409": "Water to Drain",
    "02410": "DORT",
	"025F101": "Hot Oil 025F-101",
	}
func getCachedDataJSON(index int) (string, error) {
	jsonStr, err := getCachedData(index)
	if err != nil {
		return "", err
	}

	// Parse JSON
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return "", fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	samples, ok := data["samples"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid samples format")
	}

	// Inject sample names
	for code, v := range samples {
		if name, exists := sampleNames[code]; exists {
			entry, ok := v.(map[string]interface{})
			if ok {
				entry["sampleName"] = name
				samples[code] = entry
			}
		}
	}

	data["samples"] = samples

	// Convert back to JSON
	finalJSON, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %w", err)
	}

	return string(finalJSON), nil
}

func getCachedDataCSV(index int) (string, error) {
	jsonStr, err := getCachedData(index)
	if err != nil {
		return "", err
	}

	var shiftData struct {
		Samples map[string]interface{} `json:"samples"`
		Shift   string                 `json:"shift"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &shiftData); err != nil {
		return "", fmt.Errorf("failed to decode JSON: %v", err)
	}

	var sb strings.Builder
	sb.WriteString("SampleID;SampleName;Property;Value\n")

	// Collect and sort SampleIDs
	var codes []string
	for code := range shiftData.Samples {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	for _, code := range codes {
		paramMapIface := shiftData.Samples[code]
		paramMap, ok := paramMapIface.(map[string]interface{})
		if !ok {
			fmt.Printf("Skipping non-map: %T\n", paramMapIface)
			continue
		}

		// Map property -> value
		propValueMap := make(map[string]string)
		for paramName, valIface := range paramMap {
			valMap, ok := valIface.(map[string]interface{})
			if !ok {
				continue
			}

			value := fmt.Sprintf("%v", valMap["value"])
			unit := fmt.Sprintf("%v", valMap["unit"])
			if unit != "" {
				value += " " + unit
			}
			propValueMap[paramName] = value
		}

		// Sort properties
		var propNames []string
		for prop := range propValueMap {
			propNames = append(propNames, prop)
		}
		sort.Strings(propNames)

		// Build sorted value slice
		var sortedValues []string
		for _, prop := range propNames {
			sortedValues = append(sortedValues, propValueMap[prop])
		}

		sampleName := sampleNames[code]
		if sampleName == "" {
			sampleName = code // fallback
		}

		sb.WriteString(fmt.Sprintf(`"%s";"%s";"%s";"%s"`+"\n",
			code, sampleName, strings.Join(propNames, "#"), strings.Join(sortedValues, "#")))
	}

	return sb.String(), nil
}
func getAllStoredDataJSON() (string, error) {
	mu.Lock()
	defer mu.Unlock()

	result := make(map[string]interface{})

	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("shifts"))
		if b == nil {
			return fmt.Errorf("bucket 'shifts' not found")
		}

		return b.ForEach(func(k, v []byte) error {
			key := string(k)

			var item ShiftCacheItem
			if err := json.Unmarshal(v, &item); err != nil {
				fmt.Printf("⚠️ Failed to unmarshal ShiftCacheItem for key %s: %v\n", key, err)
				return nil
			}

			var data map[string]interface{}
			if err := json.Unmarshal([]byte(item.Data), &data); err != nil {
				fmt.Printf("⚠️ Failed to parse JSON data for key %s: %v\n", key, err)
				return nil
			}

			// parse key -> "index_date"
			parts := strings.SplitN(key, "_", 2)
			if len(parts) != 2 {
				fmt.Printf("⚠️ Invalid key format: %s\n", key)
				return nil
			}

			indexPart := parts[0]
			datePart := parts[1]

			// convert index to shift name
			shiftName := "Unknown"
			switch indexPart {
			case "0":
				shiftName = "Malam"
			case "1":
				shiftName = "Pagi"
			case "2":
				shiftName = "Sore"
			}

			// ensure date group exists
			if _, ok := result[datePart]; !ok {
				result[datePart] = make(map[string]interface{})
			}

			dayMap := result[datePart].(map[string]interface{})

			// extract samples
			samples, ok := data["samples"].(map[string]interface{})
			if !ok {
				fmt.Printf("⚠️ No 'samples' field for key %s\n", key)
				return nil
			}

			// inject sample names
			for code, v := range samples {
				if name, exists := sampleNames[code]; exists {
					entry, ok := v.(map[string]interface{})
					if ok {
						entry["sampleName"] = name
						samples[code] = entry
					}
				}
			}

			// store under that shift for that date
			dayMap[shiftName] = samples
			result[datePart] = dayMap

			return nil
		})
	})

	if err != nil {
		return "", fmt.Errorf("failed to read from DB: %w", err)
	}

	finalJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal final JSON: %w", err)
	}

	return string(finalJSON), nil
}


func handleJSONAll() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := getAllStoredDataJSON()
		if err != nil {
			http.Error(w, fmt.Sprintf("Error fetching data: %v", err), http.StatusInternalServerError)
			return
		}
		writeJSON(w, result)
	}
}

func handleSampleJSON(index int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := getCachedDataJSON(index)
		if err != nil {
			http.Error(w, fmt.Sprintf("Error fetching data: %v", err), http.StatusInternalServerError)
			return
		}
		writeJSON(w, result)
	}
}
func handleSampleCSV(index int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := getCachedDataCSV(index)
		if err != nil {
			http.Error(w, fmt.Sprintf("Error generating CSV: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(result))
	}
}


func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")

	switch v := data.(type) {
	case string:
		// already JSON, write directly
		_, err := w.Write([]byte(v))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	default:
		// normal struct/map, encode normally
		if err := json.NewEncoder(w).Encode(v); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func enableCORS(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
        w.Header().Set("Access-Control-Allow-Credentials", "true")

        if r.Method == "OPTIONS" {
            w.WriteHeader(http.StatusOK)
            return
        }

        next.ServeHTTP(w, r)
    })
}



var logger service.Logger

type program struct {
	httpServer *http.Server
}

func (p *program) Start(s service.Service) error {
	if err := initDB(); err != nil {
		logger.Errorf("Failed to initialize database: %v", err)
		return err
	}

	// ✅ Use a custom ServeMux so we can wrap it with CORS
	mux := http.NewServeMux()
	mux.HandleFunc("/malam", handleSampleJSON(0))
	mux.HandleFunc("/pagi", handleSampleJSON(1))
	mux.HandleFunc("/sore", handleSampleJSON(2))
	mux.HandleFunc("/all", handleJSONAll())
	mux.HandleFunc("/malamcsv", handleSampleCSV(0))
	mux.HandleFunc("/pagicsv", handleSampleCSV(1))
	mux.HandleFunc("/sorecsv", handleSampleCSV(2))

	// ✅ Wrap the mux with your CORS middleware
	handlerWithCORS := enableCORS(mux)

	// ✅ Assign the wrapped handler to your server
	p.httpServer = &http.Server{
		Addr:    ":53238",
		Handler: handlerWithCORS,
	}

	go p.run()
	return nil
}

func (p *program) run() {
	logger.Info("Server running on http://localhost:53238")
	if err := p.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Errorf("HTTP server ListenAndServe: %v", err)
	}
}

func (p *program) Stop(s service.Service) error {
	logger.Info("Stopping service...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := p.httpServer.Shutdown(ctx); err != nil {
		logger.Warningf("HTTP server Shutdown: %v", err)
	}
	closeDB()
	logger.Info("Service stopped.")
	return nil
}

func main() {
	svcConfig := &service.Config{
		Name:        "Labware API",
		DisplayName: "Labware API",
		Description: "Labware API",
	}

	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		log.Fatal(err)
	}
	logger, err = s.Logger(nil)
	if err != nil {
		log.Fatal(err)
	}

	// Check for service control commands
	if len(os.Args) > 1 {
		cmd := os.Args[1]
		switch cmd {
		case "install":
			err = s.Install()
			if err != nil {
				log.Fatalf("Failed to install service: %v", err)
			}
			fmt.Println("Service installed.")
			return
		case "uninstall":
			err = s.Uninstall()
			if err != nil {
				log.Fatalf("Failed to uninstall service: %v", err)
			}
			fmt.Println("Service uninstalled.")
			return
		case "start":
			s.Start()
			return
		case "stop":
			s.Stop()
			return
		}
	}

	// Only run the service here
	if err = s.Run(); err != nil {
		logger.Error(err)
	}
}