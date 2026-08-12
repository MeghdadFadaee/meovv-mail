package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/meovv-mail/meovv-mail/internal/app"
	"github.com/meovv-mail/meovv-mail/internal/store"
)

const (
	version         = "0.1.0"
	stalwartVersion = "0.16.17"
	stalwartDigest  = "sha256:a8108e19bd927e172d4d8c128907b8dfc93fd180ae8ee07dccdd42cb97eb9dfa"
	// Replaced by the release key when a compatibility manifest is cut.
	compatibilityPublicKey = "LcI9Tk7sfc6//uMsUiubzg+PY+HCJOZGPV1RVN+JJKs"
)

var volumeArchives = map[string]string{
	"meovv-mail-app-data":        "meovv-data.tgz",
	"meovv-mail-stalwart-config": "stalwart-config.tgz",
	"meovv-mail-stalwart-data":   "stalwart-data.tgz",
}

type backupManifest struct {
	Format          int               `json:"format"`
	CreatedAt       time.Time         `json:"created_at"`
	MEOVVVersion    string            `json:"meovv_version"`
	StalwartVersion string            `json:"stalwart_version"`
	Files           map[string]string `json:"sha256"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "init":
		err = initialize(os.Args[2:])
	case "doctor":
		err = doctor(os.Args[2:])
	case "create-api-key":
		err = createAPIKey(os.Args[2:])
	case "configure-tls":
		err = configureTLS(os.Args[2:])
	case "backup":
		err = backup(os.Args[2:])
	case "restore":
		err = restore(os.Args[2:])
	case "upgrade":
		err = upgrade(os.Args[2:])
	case "reset-password":
		err = resetPassword(os.Args[2:])
	case "harden":
		err = harden(os.Args[2:])
	case "version":
		fmt.Printf("mailctl %s (Stalwart %s, %s)\n", version, stalwartVersion, stalwartDigest)
	case "help", "--help", "-h":
		usage()
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "mailctl:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`MEOVV Mail appliance utility

Usage: mailctl <command> [options]

  init             Create .env and one-time installation secrets
  doctor           Check DNS, ports, files, and the running appliance
  create-api-key   Create a hashed REST API key (run inside app container)
  configure-tls    Register the Certbot certificate with Stalwart
  backup           Stop mail writers and create a checksummed backup
  restore          Verify and restore a same-release backup
  upgrade          Verify compatibility, back up, and apply an approved release
  reset-password   Enable a temporary recovery administrator credential
  harden           Remove the recovery credential and restart Stalwart
  version          Print the appliance and pinned Stalwart versions
`)
}

func initialize(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	hostname := fs.String("hostname", "", "public mail hostname")
	dir := fs.String("directory", ".", "Compose bundle directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !dnsName(*hostname) {
		return errors.New("--hostname must be a fully-qualified DNS name")
	}
	root, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(root, ".env")); err == nil {
		return errors.New(".env already exists; initialization is intentionally non-destructive")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	secretDir := filepath.Join(root, "secrets")
	if err = os.MkdirAll(secretDir, 0o700); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(secretDir, "tls"), 0o700); err != nil {
		return err
	}
	values := map[string]string{
		"bootstrap_token":         randomSecret(32),
		"session_key":             randomSecret(32),
		"internal_webhook_secret": randomSecret(32),
		"api_submission_password": randomSecret(32),
	}
	for name, value := range values {
		if err = writeExclusive(filepath.Join(secretDir, name), []byte(value+"\n"), 0o600); err != nil {
			return err
		}
	}
	recovery := "admin:" + randomSecret(24)
	env := fmt.Sprintf("MEOVV_VERSION=%s\nMAIL_HOSTNAME=%s\nMEOVV_HTTP_BIND=127.0.0.1:8080\nSTALWART_HTTP_BIND=127.0.0.1:8081\nSTALWART_RECOVERY_ADMIN=%s\nAPI_SUBMISSION_USER=api-sender@%s\n", version, *hostname, recovery, strings.TrimPrefix(*hostname, "mail."))
	if err = writeExclusive(filepath.Join(root, ".env"), []byte(env), 0o600); err != nil {
		return err
	}
	fmt.Printf("Initialized %s\nBootstrap token: %s\nTemporary Stalwart recovery user: admin\nTemporary Stalwart recovery password: %s\n\nStore these values securely. Configure host Nginx/Certbot, run 'docker compose up -d --build --remove-orphans', complete setup, then run 'mailctl configure-tls' before 'mailctl harden'.\n", root, values["bootstrap_token"], strings.TrimPrefix(recovery, "admin:"))
	return nil
}

func doctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	hostname := fs.String("hostname", "", "public mail hostname; defaults to .env")
	dir := fs.String("directory", ".", "Compose bundle directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, _ := filepath.Abs(*dir)
	env, _ := readEnv(filepath.Join(root, ".env"))
	if *hostname == "" {
		*hostname = env["MAIL_HOSTNAME"]
	}
	failed := false
	check := func(name string, err error) {
		if err != nil {
			failed = true
			fmt.Printf("WARN  %-18s %v\n", name, err)
		} else {
			fmt.Printf("OK    %s\n", name)
		}
	}
	_, err := os.Stat(filepath.Join(root, "compose.yaml"))
	check("Compose bundle", err)
	_, err = os.Stat(filepath.Join(root, "secrets", "session_key"))
	check("Secrets", err)
	_, err = os.Stat(filepath.Join(root, "secrets", "tls", "fullchain.pem"))
	check("TLS certificate", err)
	_, err = os.Stat(filepath.Join(root, "secrets", "tls", "privkey.pem"))
	check("TLS private key", err)
	_, err = os.Stat(filepath.Join(root, "secrets", "tls", "certificate_id"))
	check("Stalwart TLS link", err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupHost(ctx, *hostname)
	if err == nil && len(addresses) == 0 {
		err = errors.New("no A or AAAA record")
	}
	check("A/AAAA", err)
	domain := strings.TrimPrefix(*hostname, "mail.")
	mx, mxErr := net.DefaultResolver.LookupMX(ctx, domain)
	if mxErr == nil {
		found := false
		for _, record := range mx {
			if strings.EqualFold(strings.TrimSuffix(record.Host, "."), *hostname) {
				found = true
			}
		}
		if !found {
			mxErr = fmt.Errorf("MX for %s does not point to %s", domain, *hostname)
		}
	}
	check("MX", mxErr)
	client := &net.Dialer{Timeout: 2 * time.Second}
	conn, httpErr := client.DialContext(ctx, "tcp", net.JoinHostPort(*hostname, "443"))
	if conn != nil {
		conn.Close()
	}
	check("HTTPS reachability", httpErr)
	if _, err = exec.LookPath("docker"); err == nil {
		err = run(root, "docker", "compose", "config", "--quiet")
	}
	check("Compose validation", err)
	if failed {
		return errors.New("one or more checks need attention; PTR, port 25 reachability, and IP reputation must also be verified with your hosting provider")
	}
	fmt.Println("Remember to confirm PTR, outbound port 25, SPF, DKIM, DMARC, and IP reputation externally.")
	return nil
}

func createAPIKey(args []string) error {
	fs := flag.NewFlagSet("create-api-key", flag.ContinueOnError)
	dbPath := fs.String("db", "/var/lib/meovv-mail/meovv.sqlite", "MEOVV SQLite path")
	name := fs.String("name", "Application", "key name")
	scopes := fs.String("scopes", "messages.send,messages.status", "comma-separated scopes")
	senders := fs.String("senders", "", "comma-separated approved senders")
	rate := fs.Int("rate", 60, "submissions per minute")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*senders) == "" {
		return errors.New("--senders is required")
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	secret := app.RandomID("meovv_")
	key := store.APIKey{ID: app.RandomID("key_"), Name: *name, Prefix: secret[:14], Scopes: splitCSV(*scopes), AllowedSenders: splitCSV(*senders), RateLimit: *rate, CreatedAt: time.Now().UTC()}
	if err = db.CreateAPIKey(context.Background(), key, secret); err != nil {
		return err
	}
	fmt.Printf("API key created. Copy it now; it cannot be recovered.\n%s\n", secret)
	return nil
}

func configureTLS(args []string) error {
	fs := flag.NewFlagSet("configure-tls", flag.ContinueOnError)
	dir := fs.String("directory", ".", "Compose bundle directory")
	endpoint := fs.String("url", "http://127.0.0.1:8081/api", "loopback Stalwart management endpoint")
	certificatePath := fs.String("certificate-path", "/etc/stalwart/tls/fullchain.pem", "certificate path inside the Stalwart container")
	privateKeyPath := fs.String("private-key-path", "/etc/stalwart/tls/privkey.pem", "private key path inside the Stalwart container")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, _ := filepath.Abs(*dir)
	managementURL, err := url.Parse(*endpoint)
	if err != nil || managementURL.Scheme != "http" || managementURL.Path != "/api" || managementURL.User != nil || managementURL.RawQuery != "" || managementURL.Fragment != "" {
		return errors.New("--url must be a loopback HTTP URL ending in /api")
	}
	managementHost := managementURL.Hostname()
	managementIP := net.ParseIP(managementHost)
	if managementHost != "localhost" && (managementIP == nil || !managementIP.IsLoopback()) {
		return errors.New("--url must use localhost or a loopback IP so the recovery credential is never sent over a network")
	}
	for _, name := range []string{"fullchain.pem", "privkey.pem"} {
		if _, err := os.Stat(filepath.Join(root, "secrets", "tls", name)); err != nil {
			return fmt.Errorf("secrets/tls/%s is unavailable; copy the Certbot lineage first: %w", name, err)
		}
	}
	env, err := readEnv(filepath.Join(root, ".env"))
	if err != nil {
		return err
	}
	recovery := env["STALWART_RECOVERY_ADMIN"]
	if parts := strings.SplitN(recovery, ":", 2); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return errors.New("STALWART_RECOVERY_ADMIN is required; run configure-tls before mailctl harden")
	}
	idPath := filepath.Join(root, "secrets", "tls", "certificate_id")
	idBytes, readErr := os.ReadFile(idPath)
	certificateID := strings.TrimSpace(string(idBytes))
	certificate := map[string]any{"@type": "File", "filePath": *certificatePath}
	privateKey := map[string]any{"@type": "File", "filePath": *privateKeyPath}
	var payload map[string]any
	creating := errors.Is(readErr, os.ErrNotExist) || certificateID == ""
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	if creating {
		payload = jmapPayload("x:Certificate/set", map[string]any{"create": map[string]any{"certbot": map[string]any{"certificate": certificate, "privateKey": privateKey}}}, "certificate")
	} else {
		payload = jmapPayload("x:Certificate/set", map[string]any{"update": map[string]any{certificateID: map[string]any{"certificate": certificate, "privateKey": privateKey}}}, "certificate")
	}
	raw, err := callStalwart(*endpoint, recovery, payload)
	if err != nil {
		return err
	}
	if creating {
		certificateID, err = createdObjectID(raw, "certbot")
	} else {
		err = jmapSucceeded(raw)
	}
	if err != nil {
		return fmt.Errorf("register Certbot certificate: %w", err)
	}
	if creating {
		if err = writeExclusive(idPath, []byte(certificateID+"\n"), 0o600); err != nil {
			return err
		}
	}
	payload = jmapPayload("x:SystemSettings/set", map[string]any{"update": map[string]any{"singleton": map[string]any{"defaultCertificateId": certificateID}}}, "settings")
	raw, err = callStalwart(*endpoint, recovery, payload)
	if err != nil {
		return err
	}
	if err = jmapSucceeded(raw); err != nil {
		return fmt.Errorf("select default certificate: %w", err)
	}
	if err = run(root, "docker", "compose", "restart", "stalwart"); err != nil {
		return fmt.Errorf("certificate registered, but Stalwart could not be restarted: %w", err)
	}
	fmt.Println("Certbot certificate registered as Stalwart's default TLS certificate.")
	return nil
}

func jmapPayload(method string, arguments map[string]any, callID string) map[string]any {
	return map[string]any{
		"using":       []string{"urn:ietf:params:jmap:core", "urn:stalwart:jmap"},
		"methodCalls": []any{[]any{method, arguments, callID}},
	}
}

func callStalwart(endpoint, recovery string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(recovery, ":", 2)
	req.SetBasicAuth(parts[0], parts[1])
	req.Header.Set("Content-Type", "application/json")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contact Stalwart management endpoint: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode/100 != 2 {
		return nil, fmt.Errorf("Stalwart returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func jmapMethodBody(raw []byte) (string, json.RawMessage, error) {
	var response struct {
		MethodResponses [][]json.RawMessage `json:"methodResponses"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", nil, err
	}
	if len(response.MethodResponses) != 1 || len(response.MethodResponses[0]) < 2 {
		return "", nil, fmt.Errorf("unexpected JMAP response: %s", strings.TrimSpace(string(raw)))
	}
	var method string
	if err := json.Unmarshal(response.MethodResponses[0][0], &method); err != nil {
		return "", nil, err
	}
	return method, response.MethodResponses[0][1], nil
}

func jmapSucceeded(raw []byte) error {
	method, body, err := jmapMethodBody(raw)
	if err != nil {
		return err
	}
	if method == "error" || bytes.Contains(body, []byte(`"notUpdated"`)) || bytes.Contains(body, []byte(`"notCreated"`)) {
		return fmt.Errorf("Stalwart rejected the change: %s", strings.TrimSpace(string(body)))
	}
	return nil
}

func createdObjectID(raw []byte, clientID string) (string, error) {
	method, body, err := jmapMethodBody(raw)
	if err != nil {
		return "", err
	}
	if method == "error" {
		return "", fmt.Errorf("Stalwart rejected the change: %s", strings.TrimSpace(string(body)))
	}
	var result struct {
		Created map[string]struct {
			ID string `json:"id"`
		} `json:"created"`
		NotCreated map[string]json.RawMessage `json:"notCreated"`
	}
	if err = json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if failure := result.NotCreated[clientID]; len(failure) > 0 {
		return "", fmt.Errorf("Stalwart rejected the certificate: %s", strings.TrimSpace(string(failure)))
	}
	id := result.Created[clientID].ID
	if id == "" {
		return "", fmt.Errorf("certificate id missing from response: %s", strings.TrimSpace(string(body)))
	}
	return id, nil
}

func backup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	dir := fs.String("directory", ".", "Compose bundle directory")
	destination := fs.String("output", "backups", "backup destination")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, _ := filepath.Abs(*dir)
	outRoot := *destination
	if !filepath.IsAbs(outRoot) {
		outRoot = filepath.Join(root, outRoot)
	}
	path := filepath.Join(outRoot, time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	if err := run(root, "docker", "compose", "stop", "meovv", "stalwart"); err != nil {
		return err
	}
	defer func() { _ = run(root, "docker", "compose", "up", "-d", "stalwart", "meovv") }()
	names := make([]string, 0, len(volumeArchives))
	for volume := range volumeArchives {
		names = append(names, volume)
	}
	sort.Strings(names)
	for _, volume := range names {
		archive := volumeArchives[volume]
		if err := run(root, "docker", "run", "--rm", "-v", volume+":/source:ro", "-v", path+":/backup", "alpine:3.22", "tar", "-czf", "/backup/"+archive, "-C", "/source", "."); err != nil {
			return err
		}
	}
	if err := archiveConfiguration(root, filepath.Join(path, "configuration.tgz")); err != nil {
		return err
	}
	manifest := backupManifest{Format: 1, CreatedAt: time.Now().UTC(), MEOVVVersion: version, StalwartVersion: stalwartVersion, Files: map[string]string{}}
	for _, file := range append(mapsValues(volumeArchives), "configuration.tgz") {
		digest, err := fileDigest(filepath.Join(path, file))
		if err != nil {
			return err
		}
		manifest.Files[file] = digest
	}
	raw, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(path, "manifest.json"), append(raw, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Println("Backup complete:", path)
	return nil
}

func restore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	dir := fs.String("directory", ".", "clean Compose bundle directory")
	from := fs.String("from", "", "backup directory")
	yes := fs.Bool("yes", false, "confirm destructive replacement of appliance data")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*yes || *from == "" {
		return errors.New("restore requires --from and --yes; it replaces all appliance volumes")
	}
	root, _ := filepath.Abs(*dir)
	backupDir, _ := filepath.Abs(*from)
	manifest, err := verifyBackup(backupDir)
	if err != nil {
		return err
	}
	if manifest.MEOVVVersion != version || manifest.StalwartVersion != stalwartVersion {
		return fmt.Errorf("backup release is MEOVV %s / Stalwart %s; this binary is %s / %s", manifest.MEOVVVersion, manifest.StalwartVersion, version, stalwartVersion)
	}
	if err = run(root, "docker", "compose", "stop", "meovv", "stalwart"); err != nil {
		return err
	}
	for volume, archive := range volumeArchives {
		script := "find /target -mindepth 1 -maxdepth 1 -exec rm -rf -- {} + && tar -xzf /backup/" + archive + " -C /target"
		if err = run(root, "docker", "run", "--rm", "-v", volume+":/target", "-v", backupDir+":/backup:ro", "alpine:3.22", "sh", "-c", script); err != nil {
			return err
		}
	}
	if err = restoreConfiguration(root, filepath.Join(backupDir, "configuration.tgz")); err != nil {
		return err
	}
	if err = run(root, "docker", "compose", "up", "-d"); err != nil {
		return err
	}
	fmt.Println("Restore complete. Run 'mailctl doctor' before accepting traffic.")
	return nil
}

func upgrade(args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	dir := fs.String("directory", ".", "Compose bundle directory")
	manifestPath := fs.String("manifest", "release/compatibility.json", "signed compatibility manifest")
	signaturePath := fs.String("signature", "release/compatibility.sig", "manifest signature")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, _ := filepath.Abs(*dir)
	manifest := *manifestPath
	if !filepath.IsAbs(manifest) {
		manifest = filepath.Join(root, manifest)
	}
	signature := *signaturePath
	if !filepath.IsAbs(signature) {
		signature = filepath.Join(root, signature)
	}
	if err := verifyCompatibility(manifest, signature); err != nil {
		return err
	}
	if err := doctor([]string{"--directory", root}); err != nil {
		return fmt.Errorf("pre-upgrade diagnostics: %w", err)
	}
	if err := backup([]string{"--directory", root}); err != nil {
		return fmt.Errorf("pre-upgrade backup: %w", err)
	}
	if err := run(root, "docker", "compose", "pull"); err != nil {
		return err
	}
	if err := run(root, "docker", "compose", "build", "--pull", "meovv"); err != nil {
		return err
	}
	return run(root, "docker", "compose", "up", "-d")
}

func resetPassword(args []string) error {
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	dir := fs.String("directory", ".", "Compose bundle directory")
	username := fs.String("user", "recovery-admin", "temporary recovery username")
	password := fs.String("password", "", "temporary recovery password; generated when omitted")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *password == "" {
		*password = randomSecret(24)
	}
	root, _ := filepath.Abs(*dir)
	if err := setEnv(filepath.Join(root, ".env"), "STALWART_RECOVERY_ADMIN", *username+":"+*password); err != nil {
		return err
	}
	if err := run(root, "docker", "compose", "up", "-d", "--force-recreate", "stalwart"); err != nil {
		return err
	}
	fmt.Printf("Temporary recovery access enabled for %s. Sign in, reset the permanent account, verify it, then immediately run 'mailctl harden'.\nPassword: %s\n", *username, *password)
	return nil
}

func harden(args []string) error {
	fs := flag.NewFlagSet("harden", flag.ContinueOnError)
	dir := fs.String("directory", ".", "Compose bundle directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, _ := filepath.Abs(*dir)
	if err := setEnv(filepath.Join(root, ".env"), "STALWART_RECOVERY_ADMIN", ""); err != nil {
		return err
	}
	if err := run(root, "docker", "compose", "up", "-d", "--force-recreate", "stalwart", "meovv"); err != nil {
		return err
	}
	fmt.Println("Recovery administrator removed from the running environment.")
	return nil
}

func verifyBackup(path string) (backupManifest, error) {
	raw, err := os.ReadFile(filepath.Join(path, "manifest.json"))
	if err != nil {
		return backupManifest{}, err
	}
	var manifest backupManifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return manifest, err
	}
	if manifest.Format != 1 {
		return manifest, fmt.Errorf("unsupported backup format %d", manifest.Format)
	}
	for file, expected := range manifest.Files {
		actual, err := fileDigest(filepath.Join(path, file))
		if err != nil {
			return manifest, err
		}
		if !strings.EqualFold(actual, expected) {
			return manifest, fmt.Errorf("checksum mismatch for %s", file)
		}
	}
	return manifest, nil
}

func verifyCompatibility(manifestPath, signaturePath string) error {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	sigText, err := os.ReadFile(signaturePath)
	if err != nil {
		return err
	}
	publicKey, err := base64.RawStdEncoding.DecodeString(compatibilityPublicKey)
	if err != nil {
		return err
	}
	signature, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(sigText)))
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), raw, signature) {
		return errors.New("compatibility manifest signature is invalid")
	}
	var manifest struct {
		MEOVVVersion string `json:"meovv_version"`
		Stalwart     struct {
			Version string `json:"version"`
			Digest  string `json:"digest"`
		} `json:"stalwart"`
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	if manifest.MEOVVVersion != version || manifest.Stalwart.Version != stalwartVersion || manifest.Stalwart.Digest != stalwartDigest {
		return errors.New("compatibility manifest does not approve the versions embedded in this release")
	}
	return nil
}

func archiveConfiguration(root, destination string) error {
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	paths := []string{".env", "compose.yaml"}
	_ = filepath.WalkDir(filepath.Join(root, "secrets"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() {
			rel, _ := filepath.Rel(root, path)
			paths = append(paths, rel)
		}
		return walkErr
	})
	for _, relative := range paths {
		path := filepath.Join(root, relative)
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		header, _ := tar.FileInfoHeader(info, "")
		header.Name = filepath.ToSlash(relative)
		if err = tw.WriteHeader(header); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, file)
		file.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func restoreConfiguration(root, archive string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(header.Name)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") || (clean != ".env" && clean != "compose.yaml" && !strings.HasPrefix(clean, "secrets/")) {
			return fmt.Errorf("unsafe configuration archive entry %q", header.Name)
		}
		target := filepath.Join(root, clean)
		if err = os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		mode := os.FileMode(header.Mode) & 0o700
		if mode == 0 {
			mode = 0o600
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, err = io.CopyN(out, tr, header.Size)
		out.Close()
		if err != nil {
			return err
		}
	}
}

func run(directory, name string, args ...string) error {
	command := exec.Command(name, args...)
	command.Dir = directory
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin
	return command.Run()
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err = io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func randomSecret(size int) string {
	raw := make([]byte, size)
	_, _ = rand.Read(raw)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func writeExclusive(path string, contents []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(contents)
	return err
}

func readEnv(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && !strings.HasPrefix(strings.TrimSpace(key), "#") {
			out[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return out, nil
}

func setEnv(path, key, value string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, key+"=") {
			lines[i] = key + "=" + value
			found = true
		}
	}
	if !found {
		lines = append(lines, key+"="+value)
	}
	temporary := path + ".tmp"
	if err = os.WriteFile(temporary, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func dnsName(value string) bool {
	if len(value) < 3 || len(value) > 253 || !strings.Contains(value, ".") || strings.ContainsAny(value, " /:@") {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" || strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return false
		}
	}
	return true
}

func splitCSV(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func mapsValues(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
