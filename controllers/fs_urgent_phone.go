package controllers

import (
	"PrometheusAlert/models"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"github.com/astaxie/beego"
	"github.com/astaxie/beego/logs"
)

// FSUrgentPhoneTokenRequest - Request to get tenant access token
type FSUrgentPhoneTokenRequest struct {
	AppId     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
}

// FSUrgentPhoneTokenResponse - Response from tenant access token API
type FSUrgentPhoneTokenResponse struct {
	Code               int    `json:"code"`
	Msg                string `json:"msg"`
	TenantAccessToken  string `json:"tenant_access_token"`
}

// FSUrgentPhoneMessageRequest - Request to send message
type FSUrgentPhoneMessageRequest struct {
	MsgType  string `json:"msg_type"`
	ReceiveId string `json:"receive_id"`
	Content  string `json:"content"`
}

// FSUrgentPhoneMessageResponse - Response from send message API
type FSUrgentPhoneMessageResponse struct {
	Code    int `json:"code"`
	Msg     string `json:"msg"`
	Data    struct {
		MessageId string `json:"message_id"`
	} `json:"data"`
}

// FSUrgentPhoneRequest - Request to send urgent phone call
type FSUrgentPhoneRequest struct {
	UserIdList []string `json:"user_id_list"`
}

// FSUrgentPhoneResponse - Response from urgent phone API
type FSUrgentPhoneResponse struct {
	Code int `json:"code"`
	Msg  string `json:"msg"`
}

// PostToFSUrgentPhone - Send urgent phone notification via Feishu
func PostToFSUrgentPhone(title, text, appid, appsecret, sendto, urgentPhoneOpenIds, logsign string) string {
	open := beego.AppConfig.String("open-fs-urgent-phone")
	if open != "1" {
		logs.Info(logsign, "[fs-urgent-phone]", "飞书紧急电话接口未配置未开启状态,请先配置open-fs-urgent-phone为1")
		return "飞书紧急电话接口未配置未开启状态,请先配置open-fs-urgent-phone为1"
	}

	if appid == "" || appsecret == "" {
		logs.Error(logsign, "[fs-urgent-phone]", "appid or appsecret is empty")
		return "appid or appsecret is empty"
	}

	if sendto == "" {
		logs.Error(logsign, "[fs-urgent-phone]", "sendto is empty")
		return "sendto is empty"
	}

	// Step 1: Get tenant access token
	token, err := getFSUrgentPhoneAccessToken(appid, appsecret, logsign)
	if err != nil {
		logs.Error(logsign, "[fs-urgent-phone]", "Failed to get access token: "+err.Error())
		return "Failed to get access token: " + err.Error()
	}

	// Step 2: Send text message to get message_id
	messageId, err := sendFSUrgentPhoneMessage(token, sendto, text, logsign)
	if err != nil {
		logs.Error(logsign, "[fs-urgent-phone]", "Failed to send message: "+err.Error())
		return "Failed to send message: " + err.Error()
	}

	// Step 3: Send urgent phone call if urgent_phone_openids is provided
	if urgentPhoneOpenIds != "" {
		err = sendFSUrgentPhoneCall(token, messageId, urgentPhoneOpenIds, logsign)
		if err != nil {
			logs.Error(logsign, "[fs-urgent-phone]", "Failed to send urgent phone: "+err.Error())
			return "Message sent but failed to send urgent phone: " + err.Error()
		}
		logs.Info(logsign, "[fs-urgent-phone]", "Urgent phone call sent successfully")
	}

	models.AlertToCounter.WithLabelValues("fs-urgent-phone").Add(1)
	ChartsJson.Feishu += 1

	return fmt.Sprintf("Message sent successfully, message_id: %s", messageId)
}

// getFSUrgentPhoneAccessToken - Get tenant access token from Feishu
func getFSUrgentPhoneAccessToken(appid, appsecret, logsign string) (string, error) {
	tokenReq := FSUrgentPhoneTokenRequest{
		AppId:     appid,
		AppSecret: appsecret,
	}

	b := new(bytes.Buffer)
	json.NewEncoder(b).Encode(tokenReq)
	logs.Info(logsign, "[fs-urgent-phone]", "Get token request: "+b.String())

	var tr *http.Transport
	if proxyUrl := beego.AppConfig.String("proxy"); proxyUrl != "" {
		proxy := func(_ *http.Request) (*url.URL, error) {
			return url.Parse(proxyUrl)
		}
		tr = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			Proxy:           proxy,
		}
	} else {
		tr = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	client := &http.Client{Transport: tr}
	req, err := http.NewRequest("POST", "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal", b)
	if err != nil {
		logs.Error(logsign, "[fs-urgent-phone]", err.Error())
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := client.Do(req)
	if err != nil {
		logs.Error(logsign, "[fs-urgent-phone]", err.Error())
		return "", err
	}
	defer resp.Body.Close()

	result, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logs.Error(logsign, "[fs-urgent-phone]", err.Error())
		return "", err
	}

	tokenResp := FSUrgentPhoneTokenResponse{}
	err = json.Unmarshal(result, &tokenResp)
	if err != nil {
		logs.Error(logsign, "[fs-urgent-phone]", err.Error())
		return "", err
	}

	logs.Info(logsign, "[fs-urgent-phone]", "Get token response: "+string(result))

	if tokenResp.Code != 0 || tokenResp.Msg != "ok" {
		errMsg := fmt.Sprintf("Failed to get token, code: %d, msg: %s", tokenResp.Code, tokenResp.Msg)
		logs.Error(logsign, "[fs-urgent-phone]", errMsg)
		return "", errors.New(errMsg)
	}

	return tokenResp.TenantAccessToken, nil
}

// sendFSUrgentPhoneMessage - Send text message to specified user(s)
func sendFSUrgentPhoneMessage(token, sendto, text, logsign string) (string, error) {
	// Create message content
	contentMap := map[string]string{
		"text": text,
	}
	contentBytes, _ := json.Marshal(contentMap)

	var lastMessageId string
	sendToList := strings.Split(sendto, ",")

	for _, receiveId := range sendToList {
		receiveId = strings.TrimSpace(receiveId)
		if receiveId == "" {
			continue
		}

		msgReq := FSUrgentPhoneMessageRequest{
			MsgType:  "text",
			ReceiveId: receiveId,
			Content:  string(contentBytes),
		}

		b := new(bytes.Buffer)
		json.NewEncoder(b).Encode(msgReq)
		logs.Info(logsign, "[fs-urgent-phone]", "Send message request: "+b.String())

		var tr *http.Transport
		if proxyUrl := beego.AppConfig.String("proxy"); proxyUrl != "" {
			proxy := func(_ *http.Request) (*url.URL, error) {
				return url.Parse(proxyUrl)
			}
			tr = &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				Proxy:           proxy,
			}
		} else {
			tr = &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			}
		}

		client := &http.Client{Transport: tr}
		fsUrl := "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=open_id"
		req, err := http.NewRequest("POST", fsUrl, b)
		if err != nil {
			logs.Error(logsign, "[fs-urgent-phone]", err.Error())
			return "", err
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			logs.Error(logsign, "[fs-urgent-phone]", err.Error())
			return "", err
		}
		defer resp.Body.Close()

		result, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			logs.Error(logsign, "[fs-urgent-phone]", err.Error())
			return "", err
		}

		msgResp := FSUrgentPhoneMessageResponse{}
		err = json.Unmarshal(result, &msgResp)
		if err != nil {
			logs.Error(logsign, "[fs-urgent-phone]", err.Error())
			return "", err
		}

		logs.Info(logsign, "[fs-urgent-phone]", "Send message response: "+string(result))

		if msgResp.Code != 0 {
			errMsg := fmt.Sprintf("Failed to send message, code: %d, msg: %s", msgResp.Code, msgResp.Msg)
			logs.Error(logsign, "[fs-urgent-phone]", errMsg)
			return "", errors.New(errMsg)
		}

		lastMessageId = msgResp.Data.MessageId
	}

	return lastMessageId, nil
}

// sendFSUrgentPhoneCall - Send urgent phone call to specified users
func sendFSUrgentPhoneCall(token, messageId, urgentPhoneOpenIds, logsign string) error {
	// Parse urgent phone open ids
	openIdList := strings.Split(urgentPhoneOpenIds, ",")
	var cleanedOpenIds []string
	for _, openId := range openIdList {
		openId = strings.TrimSpace(openId)
		if openId != "" {
			cleanedOpenIds = append(cleanedOpenIds, openId)
		}
	}

	if len(cleanedOpenIds) == 0 {
		logs.Error(logsign, "[fs-urgent-phone]", "No valid urgent_phone_openids provided")
		return errors.New("no valid urgent_phone_openids provided")
	}

	urgentPhoneReq := FSUrgentPhoneRequest{
		UserIdList: cleanedOpenIds,
	}

	b := new(bytes.Buffer)
	json.NewEncoder(b).Encode(urgentPhoneReq)
	logs.Info(logsign, "[fs-urgent-phone]", "Urgent phone request: "+b.String())

	var tr *http.Transport
	if proxyUrl := beego.AppConfig.String("proxy"); proxyUrl != "" {
		proxy := func(_ *http.Request) (*url.URL, error) {
			return url.Parse(proxyUrl)
		}
		tr = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			Proxy:           proxy,
		}
	} else {
		tr = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	client := &http.Client{Transport: tr}
	fsUrl := fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/messages/%s/urgent_phone", messageId)
	req, err := http.NewRequest("PATCH", fsUrl, b)
	if err != nil {
		logs.Error(logsign, "[fs-urgent-phone]", err.Error())
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		logs.Error(logsign, "[fs-urgent-phone]", err.Error())
		return err
	}
	defer resp.Body.Close()

	result, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logs.Error(logsign, "[fs-urgent-phone]", err.Error())
		return err
	}

	urgentPhoneResp := FSUrgentPhoneResponse{}
	err = json.Unmarshal(result, &urgentPhoneResp)
	if err != nil {
		logs.Error(logsign, "[fs-urgent-phone]", err.Error())
		return err
	}

	logs.Info(logsign, "[fs-urgent-phone]", "Urgent phone response: "+string(result))

	if urgentPhoneResp.Code != 0 {
		errMsg := fmt.Sprintf("Failed to send urgent phone, code: %d, msg: %s", urgentPhoneResp.Code, urgentPhoneResp.Msg)
		logs.Error(logsign, "[fs-urgent-phone]", errMsg)
		return errors.New(errMsg)
	}

	return nil
}
