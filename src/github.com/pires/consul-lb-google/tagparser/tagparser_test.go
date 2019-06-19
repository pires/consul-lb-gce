package tagparser

import (
	"testing"
)

func TestParse(t *testing.T) {
	tagParser := New("urlprefix-")

	tag := "urlprefix-cdn:noaffinity:host1.com/"

	tagInfo, err := tagParser.Parse(tag)

	if err != nil {
		t.Error(err)
	}

	if tagInfo.Tag != tag {
		t.Error("TagInfo.Tag is not equal to source tag")
	}

	if tagInfo.Cdn != true {
		t.Error("TagInfo.Cdn is wrong")
	}

	if tagInfo.Affinity != "noaffinity" {
		t.Error("TagInfo.Affinity is wrong")
	}

	if tagInfo.Host != "host1.com" {
		t.Error("TagInfo.Host is wrong")
	}

	if tagInfo.Path != "/" {
		t.Error("TagInfo.Path is wrong")
	}

	tag = "urlprefix-nocdn:ipaffinity:host2.com/test"

	tagInfo, err = tagParser.Parse(tag)

	if err != nil {
		t.Error(err)
	}

	if tagInfo.Tag != tag {
		t.Error("TagInfo.Tag is not equal to source tag")
	}

	if tagInfo.Cdn != false {
		t.Error("TagInfo.Cdn is wrong")
	}

	if tagInfo.Affinity != "ipaffinity" {
		t.Error("TagInfo.Affinity is wrong")
	}

	if tagInfo.Host != "host2.com" {
		t.Error("TagInfo.Host is wrong")
	}

	if tagInfo.Path != "/test" {
		t.Error("TagInfo.Path is wrong")
	}
}
