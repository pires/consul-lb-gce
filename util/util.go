package util

import (
	"errors"
	"io/ioutil"
	"net/http"
	"strings"
)

func SendHTTPRequest(c *http.Client, req *http.Request, successCodes []int) (*http.Response, error) {
	response, err := c.Do(req)
	if err != nil {
		return nil, err
	}

	successStatus := false
	for _, c := range successCodes {
		if c == response.StatusCode {
			successStatus = true
			break
		}
	}

	if !successStatus {
		bodyBytes, err := ioutil.ReadAll(response.Body)
		if err != nil {
			return nil, err
		}
		return nil, errors.New(string(bodyBytes))
	}

	return response, nil
}

func Zonify(zone, name string) string {
	return strings.Join([]string{zone, name}, "-")
}

func Unzonify(name string, zone string) string {
	return strings.TrimPrefix(name, zone+"-")
}

func NormalizeInstanceName(name string) string {
	return strings.Split(name, ".")[0]
}
