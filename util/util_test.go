package util

import "testing"

func TestZonify(t *testing.T) {
	expected := "us-east1-d-myname"
	actual := Zonify("us-east1-d", "myname")
	if actual != expected {
		t.Fatalf("%s is not equal %s", actual, expected)
	}
}

func TestUnzonify(t *testing.T) {
	expected := "myname"
	actual := Unzonify("us-east1-d-myname", "us-east1-d")
	if actual != expected {
		t.Fatalf("%s is not equal %s", actual, expected)
	}
}

func TestNormalizeInstanceName(t *testing.T) {
	expected := "kubernetes-minion-2"
	actual := NormalizeInstanceName("kubernetes-minion-2.c.my-proj.internal")
	if actual != expected {
		t.Fatalf("%s is not equal %s", actual, expected)
	}
}
