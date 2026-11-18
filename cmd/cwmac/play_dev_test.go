package main

import "testing"

func TestPickOutput(t *testing.T) {
	names := []string{"Speakers (Realtek)", "Speakers (USB Audio CODEC)", "Headphones"}
	if pickOutput(names, "") != -1 || pickOutput(names, "default") != -1 {
		t.Fatal("empty should be the default device")
	}
	if pickOutput(names, "wave:1") != 1 || pickOutput(names, "0") != 0 {
		t.Fatal("index was not honored")
	}
	if pickOutput(names, "wave:9") != -1 {
		t.Fatal("an index past the list should fall back")
	}
	if got := pickOutput(names, "USB Audio CODEC"); got != 1 {
		t.Fatalf("name match got %d", got)
	}
	if got := pickOutput(names, "Microphone (USB Audio CODEC)"); got != 1 {
		t.Fatalf("recording-name match got %d", got)
	}
}

func TestNativeEncoderIsTheDefault(t *testing.T) {
	t.Setenv("CWBOX_FT8TX", "")
	if !ft8TxAvailable() {
		t.Fatal("the built-in encoder was not available")
	}
	w, sent, err := ft8Wave("CQ KN0BLE EN34", 1500)
	if err != nil {
		t.Fatal(err)
	}
	if sent != "CQ KN0BLE EN34" || len(w) != 79*7680 {
		t.Fatalf("sent %q, %d samples", sent, len(w))
	}
}
