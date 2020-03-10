package main

import "testing"

func TestParse(t *testing.T) {
	tagParser := newTagParser("urlprefix-")

	tag := "urlprefix-cdn:noaffinity:host1.com/"
	tagInfo, err := tagParser.Parse(tag)
	if err != nil {
		t.Error(err)
	}
	if tagInfo.Tag != tag {
		t.Error("Tag is not equal to source tag")
	}
	if tagInfo.CDN != true {
		t.Error("Cdn is wrong")
	}
	if tagInfo.Affinity != "noaffinity" {
		t.Error("Affinity is wrong")
	}
	if tagInfo.Host != "host1.com" {
		t.Error("Host is wrong")
	}
	if tagInfo.Path != "/" {
		t.Error("Path is wrong")
	}

	tag = "urlprefix-nocdn:ipaffinity:host2.com/test"
	tagInfo, err = tagParser.Parse(tag)
	if err != nil {
		t.Error(err)
	}
	if tagInfo.Tag != tag {
		t.Error("Tag is not equal to source tag")
	}
	if tagInfo.CDN != false {
		t.Error("Cdn is wrong")
	}
	if tagInfo.Affinity != "ipaffinity" {
		t.Error("Affinity is wrong")
	}
	if tagInfo.Host != "host2.com" {
		t.Error("Host is wrong")
	}
	if tagInfo.Path != "/test" {
		t.Error("Path is wrong")
	}
}
