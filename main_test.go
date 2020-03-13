package main

import "testing"

func TestNormalizeInstanceName(t *testing.T) {
	expected := "kubernetes-minion-2"
	actual := normalizeInstanceName("kubernetes-minion-2.c.my-proj.internal")
	if actual != expected {
		t.Fatalf("%s is not equal %s", actual, expected)
	}
}

func TestMakeName(t *testing.T) {
	expected := "bs-name"
	actual := makeName("bs", "name")
	if actual != expected {
		t.Fatalf("%s is not equal %s", actual, expected)
	}

	expected = "bs-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	actual = makeName("bs", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if actual != expected {
		t.Fatalf("%s is not equal %s", actual, expected)
	}
}
