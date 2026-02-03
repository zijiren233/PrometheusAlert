package controllers

import (
	"PrometheusAlert/models"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/astaxie/beego"
	"github.com/astaxie/beego/logs"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
)

type FSAPPConf struct {
	WideScreenMode bool `json:"wide_screen_mode"`
	EnableForward  bool `json:"enable_forward"`
}

type FSAPPTe struct {
	Content string `json:"content"`
	Tag     string `json:"tag"`
}

type FSAPPElement struct {
	Tag           string         `json:"tag"`
	Text          Te             `json:"text"`
	Content       string         `json:"content"`
	FSAPPElements []FSAPPElement `json:"elements"`
}

type FSAPPTitles struct {
	Content string `json:"content"`
	Tag     string `json:"tag"`
}

type FSAPPHeaders struct {
	FSAPPTitle FSAPPTitles `json:"title"`
	Template   string      `json:"template"`
}

type FSAPPCards struct {
	FSAPPConfig   FSAPPConf      `json:"config"`
	FSAPPElements []FSAPPElement `json:"elements"`
	FSAPPHeader   FSAPPHeaders   `json:"header"`
}

type FSContentAPP struct {
	MsgType      string `json:"msg_type"`
	ReceiveId    string `json:"receive_id"` //用户传入的ID，可以是 open_id、user_id、union_id、email、chat_id
	FSAPPContent string `json:"content"`
}

func GetAccessToken(logsign string) (string, error) {
	// https://open.feishu.cn/open-apis/message/v4/batch_send/ 批量发送消息  tenant_access_token
	// 先获取 tenant_access_token
	u := TenantAccessMeg{
		AppId:     beego.AppConfig.String("FEISHU_APPID"),
		AppSecret: beego.AppConfig.String("FEISHU_APPSECRET"),
	}
	b := new(bytes.Buffer)
	json.NewEncoder(b).Encode(u)
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
	//res, err := client.Post("https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal", "application/json; charset=utf-8", b)
	res, err := http.NewRequest("POST", "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal", b)
	if err != nil {
		logs.Error(logsign, "[feishuapp]", err.Error())
		return "", err
	}
	res.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := client.Do(res)
	defer res.Body.Close()
	result, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logs.Error(logsign, "[feishuapp]", err.Error())
		return "", err
	}
	resp_json := TenantAccessResp{}
	json.Unmarshal(result, &resp_json)
	if resp_json.Msg != "ok" {
		logs.Error(logsign, "[feishuapp]", resp_json.Msg)
		return "", errors.New(resp_json.Msg)
	}
	logs.Info(logsign, "[feishuapp]", string(result))
	return resp_json.TenantAccessToken, nil
}

func PostToFeiShuApp(title, text, receiveIds, urgentPhoneOpenIds, logsign string) string {
	open := beego.AppConfig.String("open-feishuapp")
	if open != "1" {
		logs.Info(logsign, "[feishuapp]", "飞书APP接口未配置未开启状态,请先配置open-feishuapp为1")
		return "飞书APP接口未配置未开启状态,请先配置open-feishuapp为1"
	}

	// 校验：如果配置了紧急电话，receiveIds 只能是单个
	if urgentPhoneOpenIds != "" && receiveIds != "" {
		ReceiveIds := strings.Split(receiveIds, ",")
		if len(ReceiveIds) > 1 {
			errMsg := "配置错误：当启用紧急电话功能时(urgent_phone_openids不为空)，接收人(receiveIds/at参数)只能指定一个人，不能是多个"
			logs.Error(logsign, "[feishuapp]", errMsg)
			return errMsg
		}
	}

	var color string
	if strings.Count(text, "resolved") > 0 && strings.Count(text, "firing") > 0 {
		color = "orange"
	} else if strings.Count(text, "resolved") > 0 {
		color = "green"
	} else {
		color = "red"
	}
	token, err := GetAccessToken(logsign)
	if err != nil {
		logs.Error(logsign, "[feishuapp]", err.Error())
		return err.Error()
	}
	SendContent := text
	var result []byte
	var lastMessageId string
	if receiveIds != "" {
		ReceiveIds := strings.Split(receiveIds, ",")
		fsAppContent :=
			&FSAPPCards{
				FSAPPConfig: FSAPPConf{
					WideScreenMode: true,
					EnableForward:  true,
				},
				FSAPPHeader: FSAPPHeaders{
					FSAPPTitle: FSAPPTitles{
						Content: title,
						Tag:     "plain_text",
					},
					Template: color,
				},
				FSAPPElements: []FSAPPElement{
					FSAPPElement{
						Tag: "div",
						Text: Te{
							Content: SendContent,
							Tag:     "lark_md",
						},
					},
					{
						Tag: "hr",
					},
					{
						Tag: "note",
						FSAPPElements: []FSAPPElement{
							{
								Content: title,
								Tag:     "lark_md",
							},
						},
					},
				},
			}
		contentByte, _ := json.Marshal(fsAppContent)
		fmt.Println("fsAppContent: " + string(contentByte))
		for _, ReceiveId := range ReceiveIds {
			u := FSContentAPP{
				MsgType:      "interactive",
				ReceiveId:    ReceiveId,
				FSAPPContent: string(contentByte),
			}
			var ReceiveType string
			if strings.Contains(ReceiveId, "ou_") {
				ReceiveType = "open_id"
			} else if strings.Contains(ReceiveId, "on_") {
				ReceiveType = "union_id"
			} else if strings.Contains(ReceiveId, "oc_") {
				ReceiveType = "chat_id"
			} else if strings.Contains(ReceiveId, "@") {
				ReceiveType = "email"
			} else {
				ReceiveType = "user_id"
			}
			b := new(bytes.Buffer)
			json.NewEncoder(b).Encode(u)
			logs.Info(logsign, "[feishuapp]", b)
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
			FSUrl := fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=%s", ReceiveType)
			req, err := http.NewRequest("POST", FSUrl, b)
			if err != nil {
				logs.Error(logsign, "[feishuapp]", title+": "+err.Error())
			}
			req.Header.Set("Content-Type", "application/json; charset=utf-8")
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := client.Do(req)
			if err != nil {
				logs.Error(logsign, "[feishuapp]", err.Error())
			}
			defer resp.Body.Close()
			msgResult, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				logs.Error(logsign, "[feishuapp]", title+": "+err.Error())
			}
			models.AlertToCounter.WithLabelValues("feishuapp").Add(1)
			ChartsJson.Feishu += 1
			logs.Info(logsign, "[feishuapp]", title+": "+string(msgResult))

			// 解析 message_id 用于紧急电话功能
			type MsgResponse struct {
				Code int `json:"code"`
				Data struct {
					MessageId string `json:"message_id"`
				} `json:"data"`
			}
			var msgResp MsgResponse
			if err := json.Unmarshal(msgResult, &msgResp); err == nil && msgResp.Code == 0 && msgResp.Data.MessageId != "" {
				lastMessageId = msgResp.Data.MessageId
			}

			result = msgResult
		}
	}

	// 如果指定了紧急电话人员，则发送紧急电话
	if urgentPhoneOpenIds != "" && lastMessageId != "" {
		err := sendFeiShuUrgentPhone(token, lastMessageId, urgentPhoneOpenIds, logsign)
		if err != nil {
			logs.Warn(logsign, "[feishuapp] 紧急电话发送失败: "+err.Error())
			return string(result) + "\n紧急电话发送失败: " + err.Error()
		}
		logs.Info(logsign, "[feishuapp] 紧急电话发送成功")
	}

	return string(result)
}

// sendFeiShuUrgentPhone 发送飞书紧急电话
func sendFeiShuUrgentPhone(token, messageId, urgentPhoneOpenIds, logsign string) error {
	// 解析紧急电话 open ids
	openIdList := strings.Split(urgentPhoneOpenIds, ",")
	var cleanedOpenIds []string
	for _, openId := range openIdList {
		openId = strings.TrimSpace(openId)
		if openId != "" {
			cleanedOpenIds = append(cleanedOpenIds, openId)
		}
	}

	if len(cleanedOpenIds) == 0 {
		return errors.New("no valid urgent_phone_openids provided")
	}

	urgentPhoneReq := struct {
		UserIdList []string `json:"user_id_list"`
	}{
		UserIdList: cleanedOpenIds,
	}

	b := new(bytes.Buffer)
	json.NewEncoder(b).Encode(urgentPhoneReq)
	logs.Info(logsign, "[feishuapp] 紧急电话请求: "+b.String())

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
		logs.Error(logsign, "[feishuapp]", err.Error())
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		logs.Error(logsign, "[feishuapp]", err.Error())
		return err
	}
	defer resp.Body.Close()

	result, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logs.Error(logsign, "[feishuapp]", err.Error())
		return err
	}

	logs.Info(logsign, "[feishuapp] 紧急电话响应: "+string(result))

	urgentPhoneResp := struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}{}
	err = json.Unmarshal(result, &urgentPhoneResp)
	if err != nil {
		return err
	}

	if urgentPhoneResp.Code != 0 {
		return fmt.Errorf("code: %d, msg: %s", urgentPhoneResp.Code, urgentPhoneResp.Msg)
	}

	return nil
}
