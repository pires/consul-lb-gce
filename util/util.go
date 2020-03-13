package util

import (
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
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

func ParseBody(body io.ReadCloser, v interface{}) error {
	bytes, err := ioutil.ReadAll(body)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, v)
}
