package main

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
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
	req.Header.Set("Cookie", fmt.Sprintf("%s ec_aurl=L0xJTVMvbG9naW4uaHRt; lims_dsNameCookie=LabWareV6Prod; queryStringCookie=ec_eid=onclick&ec_cid=loginForm%%3AlogButton", c.jsession))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("MainPage: bad status: %s", resp.Status)
	}

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

	// fmt.Println("Step 3 (MainPage) OK")
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
	data.Set("lw.viewguid", "ecid_c10d524c1815e8faa82623d4a4b17e67")
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
	req.Header.Set("Referer", "https://apps.pertamina.com/LIMS/index.htm?init_weblims=true&ec_eid=onclick&ec_cid=loginForm%3AlogButton")
	req.Header.Set("Cookie", fmt.Sprintf("%s _ga=GA1.2.1113838315.1658209289; %s lims_dsNameCookie=LabWareV6Prod; queryStringCookie=ec_eid=onclick&ec_cid=loginForm%%3AlogButton", c.jsession, c.ecAURL))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OpenQuery: bad status: %s", resp.Status)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	var ajaxResp AjaxResponse
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

// OpenDate simulates clicking the 'Run' button to open the date popup
// Returns the "OK" button ID, the popup DOM, and the new viewstate
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
	req.Header.Set("Origin", "https://apps.pertamina.com")
	req.Header.Set("Referer", fmt.Sprintf("https://apps.pertamina.com/LIMS/%s", c.uri))
	req.Header.Set("Cookie", fmt.Sprintf("%s lims_dsNameCookie=LabWareV6Prod; queryStringCookie=ec_eid=onclick&ec_cid=loginForm%%3AlogButton; ec_aurl=L1dlYkxJTVMvZXJyb3IuaHRt;", c.jsession))

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

	viewState, exists = docVS.Find("#javax.faces.ViewState").Attr("id")
	if !exists {
		// Sometimes the ID is not on the element, but the element IS the viewstate
		viewState, exists = docVS.Find("input[name='javax.faces.ViewState']").Attr("value")
		if !exists {
			return "", nil, "", fmt.Errorf("OpenDate: could not find new ViewState")
		}
	} else {
		// Fallback if the structure is different
		viewState, _ = docVS.Find("#javax.faces.ViewState").Attr("value")
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

// castSample formats a sample group into a string
func castSample(sam []Sample) (string, error) {
	shift := detectShift(sam)
	
	// Group samples by code
	objList := make(map[string][]Sample)
	for _, s := range sam {
		objList[s.Code] = append(objList[s.Code], s)
	}

	// Create a stable order based on codes (maps are not ordered)
	var codes []string
	for code := range objList {
		codes = append(codes, code)
	}
	// Sort codes alphabetically/numerically
	// You might want a more robust numeric sort if codes are purely numeric
	
	var finalSample strings.Builder
	for _, code := range codes {
		curr := objList[code]
		sampleName := curr[0].Code
		
		var pointDetail strings.Builder
		for _, curr1 := range curr {
			str := fmt.Sprintf("%s : %s %s \n", curr1.Param, curr1.Value, curr1.Unit)
			pointDetail.WriteString(str)
		}
		finalSample.WriteString(fmt.Sprintf("<b>%s :</b>\n%s", sampleName, pointDetail.String()))
	}
	
	return fmt.Sprintf("<b>Shift : %s</b> \n%s", shift, finalSample.String()), nil
}
func castSampleJSON(sam []Sample) (string, error) {
	shift := detectShift(sam)

	// Group samples by code
	objList := make(map[string][]Sample)
	for _, s := range sam {
		objList[s.Code] = append(objList[s.Code], s)
	}

	// Build a structured JSON response
	result := map[string]interface{}{
		"shift":   shift,
		"samples": []map[string]interface{}{},
	}

	for code, samples := range objList {
		points := []map[string]string{}
		for _, s := range samples {
			points = append(points, map[string]string{
				"param": s.Param,
				"value": s.Value,
				"unit":  s.Unit,
			})
		}
		result["samples"] = append(result["samples"].([]map[string]interface{}), map[string]interface{}{
			"code":   code,
			"points": points,
		})
	}

	// Encode to JSON
	jsonBytes, err := json.Marshal(result)
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
func proceesArray(arrayIn *goquery.Selection) ([][]Sample, error) {
	var samples []Sample
	
	// The JS uses array_in[0].children[1].children
	// goquery: .Find("tbody").Children()
	arrayIn.Find("tbody").Children().Each(func(i int, row *goquery.Selection) {
		cols := row.Children()
		
		// Date parsing: "03-Nov-2025 23:00"
		dateStr := strings.TrimSpace(cols.Eq(1).Text())
		date, err := time.Parse("01/02/2006 03:04:05 PM", dateStr)
		if err != nil {
			// Try without time if it fails
			date, err = time.Parse("02-Jan-2006", dateStr)
			if err != nil {

				date = time.Now() // Use a fallback
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
		fmt.Println("Warning: proceesArray found no samples in the table.")
		// Return empty shifts to avoid panic
		return [][]Sample{{}, {}, {}}, nil
	}

	sorted, err := sortSample(samples)
	if err != nil {
		return nil, err
	}
	
	// The JS code's active path is just `return ["All", sorted];`
	return sorted, nil
}

// (The CSV parsing functions parseDataToCSV and parseToCSV were not used in getData,
// so they are omitted here for brevity unless you need them.)

// --- Main Execution Function ---

// GetData orchestrates the entire scraping process
func GetData() ([3]string, error) {
	// !!! REPLACE WITH YOUR PASSWORD !!!
	password := "XXXXXXXXXXX"
	
	client := NewLimsClient()

	fmt.Println("Starting process...")
	if err := client.One(); err != nil {
		return [3]string{}, fmt.Errorf("Step 1 (One) failed: %v", err)
	}
	
	if err := client.LoginForm("sutanto", password); err != nil {
		return [3]string{}, fmt.Errorf("Step 2 (LoginForm) failed: %v", err)
	}
	
	if err := client.MainPage(); err != nil {
		return [3]string{}, fmt.Errorf("Step 3 (MainPage) failed: %v", err)
	}
	
	if err := client.OpenQuery(); err != nil {
		return [3]string{}, fmt.Errorf("Step 4 (OpenQuery) failed: %v", err)
	}
	
	if err := client.OpenTable(); err != nil {
		return [3]string{}, fmt.Errorf("Step 5 (OpenTable) failed: %v", err)
	}
	
	// Run 1 (for dom_loc2)
	buttonID1, dom1, viewState1, err := client.OpenDate()
	if err != nil {
		return [3]string{}, fmt.Errorf("Step 6 (OpenDate 1) failed: %v", err)
	}
	
	domLoc2, vs5, lwfocus, ecfocus, onHidelink, err := client.ClickOK(buttonID1, dom1, viewState1)
	if err != nil {
		return [3]string{}, fmt.Errorf("Step 7 (ClickOK 1) failed: %v", err)
	}
	
	if err := client.OnHide(vs5, lwfocus, ecfocus, onHidelink); err != nil {
		return [3]string{}, fmt.Errorf("Step 8 (OnHide) failed: %v", err)
	}
	
	// This step was bugged in the JS. Using corrected parameters.
	if err := client.RefreshTable(); err != nil {
		return [3]string{}, fmt.Errorf("Step 9 (RefreshTable) failed: %v", err)
	}

	// Run 2 (for dom_ext)
	// The JS code does this, but then comments out the processing of dom_ext.
	// We will replicate this behavior.
	buttonID2, dom2, viewState2, err := client.OpenDate()
	if err != nil {
		return [3]string{}, fmt.Errorf("Step 10 (OpenDate 2) failed: %v", err)
	}
	
	_, _, _, _, _, err = client.ClickOK(buttonID2, dom2, viewState2)
	if err != nil {
		return [3]string{}, fmt.Errorf("Step 11 (ClickOK 2) failed: %v", err)
	}
	
	fmt.Println("Processing data...")
	tableRoot := domLoc2.Find(".dataTableInner").First()
	if tableRoot.Length() == 0 {
		return [3]string{}, fmt.Errorf("Step 12 (Process): could not find .dataTableInner in final DOM")
	}

	dataLoc2, err := proceesArray(tableRoot)
	if err != nil {
		return [3]string{}, fmt.Errorf("Step 12 (Process): proceesArray failed: %v", err)
	}

	// dataLoc2[0] = malam, dataLoc2[1] = pagi, dataLoc2[2] = sore
	castedLoc2Malam, _ := castSampleJSON(dataLoc2[0])
	castedLoc2Pagi, _ := castSampleJSON(dataLoc2[1])
	castedLoc2Sore, _ := castSampleJSON(dataLoc2[2])

	finalResultLoc2M := stringRep(castedLoc2Malam)
	finalResultLoc2P := stringRep(castedLoc2Pagi)
	finalResultLoc2S := stringRep(castedLoc2Sore)

	fmt.Println("Process finished successfully.")
	return [3]string{finalResultLoc2M, finalResultLoc2P, finalResultLoc2S}, nil
}
type Cache struct {
	Data      [3]string
	Timestamp time.Time
	mu        sync.Mutex
}

var cache Cache
var cacheDuration = 1 * time.Minute


func getCachedData() ([3]string, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if time.Since(cache.Timestamp) < cacheDuration {
		return cache.Data, nil
	}

	data, err := GetData()
	if err != nil {
		return [3]string{}, err
	}

	cache.Data = data
	cache.Timestamp = time.Now()
	return cache.Data, nil
}

func main() {
	http.HandleFunc("/malam", handleShift(0))
	http.HandleFunc("/pagi", handleShift(1))
	http.HandleFunc("/sore", handleShift(2))

	fmt.Println("Server running on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleShift(index int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		results, err := getCachedData()
		if err != nil {
			http.Error(w, fmt.Sprintf("Error fetching data: %v", err), http.StatusInternalServerError)
			return
		}
		writeJSON(w, results[index])
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