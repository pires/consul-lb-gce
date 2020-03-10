package main

import "testing"

func TestNormalizeInstanceName(t *testing.T) {
	expected := "kubernetes-minion-2"
	actual := normalizeInstanceName("kubernetes-minion-2.c.my-proj.internal")
	if actual != expected {
		t.Fatalf("%s is not equal %s", actual, expected)
	}
}
