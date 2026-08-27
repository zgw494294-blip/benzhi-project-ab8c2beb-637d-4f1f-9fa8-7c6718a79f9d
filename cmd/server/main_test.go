package main

import "testing"

func TestAddressConfiguration(t *testing.T) {
	if got, err := addressFromEnvironment(""); err != nil || got != "127.0.0.1:19081" {
		t.Fatalf("默认地址错误: %q %v", got, err)
	}
	if got, err := addressFromEnvironment("19999"); err != nil || got != "127.0.0.1:19999" {
		t.Fatalf("PORT 地址错误: %q %v", got, err)
	}
	for _, value := range []string{"0", "abc", "70000"} {
		if _, err := addressFromEnvironment(value); err == nil {
			t.Fatalf("无效 PORT %q 应被拒绝", value)
		}
	}
	for _, value := range []string{"0.0.0.0:19081", "localhost:19081", "127.0.0.1:0", "127.0.0.1:8080x"} {
		if err := validateAddress(value); err == nil {
			t.Fatalf("无效地址 %q 应被拒绝", value)
		}
	}
	if err := validateAddress("127.0.0.1:19091"); err != nil {
		t.Fatal(err)
	}
}
