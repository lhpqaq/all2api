package zed

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"
)

func RunLoginCommand() (string, error) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", fmt.Errorf("generate rsa key error: %w", err)
	}

	pubDER := x509.MarshalPKCS1PublicKey(&privKey.PublicKey)
	pubB64 := base64.RawURLEncoding.EncodeToString(pubDER)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("listen local port error: %w", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	loginURL := fmt.Sprintf("https://zed.dev/native_app_signin?native_app_port=%d&native_app_public_key=%s", port, pubB64)

	fmt.Printf("Please log in via the opened browser window.\nIf it doesn't open automatically, visit:\n%s\n\n", loginURL)

	if err := openBrowser(loginURL); err != nil {
		fmt.Printf("Failed to open browser: %v\n", err)
	}

	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		userID := r.URL.Query().Get("user_id")
		encTokenB64 := r.URL.Query().Get("access_token")

		if userID == "" || encTokenB64 == "" {
			http.Error(w, "missing parameters", http.StatusBadRequest)
			errCh <- fmt.Errorf("callback missing user_id or access_token")
			return
		}

		var cipherText []byte
		var decErr error
		if len(encTokenB64)%4 != 0 {
			cipherText, decErr = base64.RawURLEncoding.DecodeString(encTokenB64)
		} else {
			cipherText, decErr = base64.URLEncoding.DecodeString(encTokenB64)
		}

		if decErr != nil {
			http.Error(w, "invalid base64", http.StatusBadRequest)
			errCh <- fmt.Errorf("base64 decode access_token error: %w", decErr)
			return
		}

		plainText, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, privKey, cipherText, nil)
		if err != nil {
			http.Error(w, "decrypt failed", http.StatusInternalServerError)
			errCh <- fmt.Errorf("decrypt access_token error: %w", err)
			return
		}

		w.Header().Set("Location", "https://zed.dev/native_app_signin_succeeded")
		w.WriteHeader(http.StatusFound)

		resultCh <- fmt.Sprintf("%s %s", userID, string(plainText))
	})

	server := &http.Server{Handler: mux}
	go func() {
		_ = server.Serve(listener)
	}()

	select {
	case res := <-resultCh:
		go func() {
			time.Sleep(1 * time.Second)
			_ = server.Shutdown(context.Background())
		}()
		return res, nil
	case err := <-errCh:
		_ = server.Shutdown(context.Background())
		return "", err
	case <-time.After(5 * time.Minute):
		_ = server.Shutdown(context.Background())
		return "", fmt.Errorf("login timeout after 5 minutes")
	}
}

func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start"}
	case "darwin":
		cmd = "open"
	default:
		cmd = "xdg-open"
	}
	args = append(args, url)
	return exec.Command(cmd, args...).Start()
}
