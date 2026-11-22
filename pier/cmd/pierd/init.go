package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"pier/internal/auth"
	"pier/internal/dnscheck"
	"pier/internal/store"
)

// initCmd is pierd init: checks the machine, creates the owner's account, and
// sets Pier's address and base domain, checking the DNS records for them.
// It's run once, over SSH, right after the install script.
func initCmd(args []string) int {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	data := dataFlag(fs)
	email := fs.String("email", "", "the owner's email (asked if not given)")
	pwStdin := fs.Bool("password-stdin", false, "read the password from stdin, for scripts")
	address := fs.String("address", "", "Pier's address: a domain like pier.example.com, or \"ip\" for the server's IP")
	base := fs.String("base-domain", "", "the domain project addresses go under, or \"none\"")
	ipFlag := fs.String("ip", "", "this server's public IPv4 address, if the machine can't see it")
	yes := fs.Bool("yes", false, "don't ask questions or wait for DNS: take the defaults")
	fs.Parse(args)

	t := newTTY()
	if *yes || *pwStdin {
		t.interactive = false
	}
	ctx, stop := signalContext()
	defer stop()

	if err := os.MkdirAll(*data, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "pierd:", err)
		return 1
	}
	defer chownDB(*data)
	st, err := store.Open(layout(*data).db)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pierd: open the database:", err)
		return 1
	}
	closed := false
	closeStore := func() {
		if !closed {
			closed = true
			st.Close()
		}
	}
	defer closeStore()
	if owner, err := st.Owner(ctx); err == nil {
		t.printf("Pier is already set up here, and %s is its owner.\n", owner.Email)
		t.printf("  Forgot the password?  pierd reset-password\n")
		return 1
	}

	ip := *ipFlag
	if ip == "" {
		var err error
		ip, err = dnscheck.PublicIP()
		if errors.Is(err, dnscheck.ErrNoPublicIP) && t.interactive {
			t.printf("\nThis machine only sees a private address (%s), as on EC2 or Lightsail.\n", ip)
			ip, _ = t.ask("  Its public IPv4 address: ")
		}
	}
	if net.ParseIP(ip).To4() == nil {
		fmt.Fprintln(os.Stderr, "pierd: can't tell this server's public IPv4 address; pass it with -ip")
		return 1
	}

	t.printf("\nSetting up Pier on %s\n", ip)
	checkMachine(ctx, t, *data, ip)

	owner, ok := createOwner(ctx, t, st, *email)
	if !ok {
		return 1
	}

	addr, baseDomain, ok := chooseAddresses(t, ip, *address, *base)
	if !ok {
		return 1
	}
	for k, v := range map[string]string{store.SettingAddress: addr, store.SettingBaseDomain: baseDomain, store.SettingPublicIP: ip} {
		if err := st.SetSetting(ctx, k, v); err != nil {
			fmt.Fprintln(os.Stderr, "pierd: save the settings:", err)
			return 1
		}
	}
	checkRecords(ctx, t, records(ip, addr, baseDomain))

	closeStore()
	chownDB(*data)
	t.println()
	startService(t, *data)
	t.heading("Pier is ready.")
	t.printf("    %shttps://%s\n", pad("Dashboard", 12), addr)
	t.printf("    %s%s\n", pad("Sign in as", 12), owner.Email)
	if baseDomain == "" {
		t.println()
		t.println("Projects need a domain for their addresses. Choose a base domain later, in Settings.")
	}
	t.printf("\nForgot your password? Run %s on this server.\n", t.dim("pierd reset-password"))
	return 0
}

// checkMachine looks at what Pier needs from the machine. Nothing here stops
// the setup: each problem is said plainly, with what to do about it.
func checkMachine(ctx context.Context, t *tty, data, ip string) {
	t.heading("Checks")

	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	out, err := exec.CommandContext(cctx, "docker", "version", "--format", "{{.Server.Version}}").Output()
	cancel()
	if v := strings.TrimSpace(string(out)); err == nil && v != "" {
		t.line(t.ok(), fmt.Sprintf("Docker %s is running", v))
	} else {
		t.line(t.warn(), "Docker isn't running. Pier builds every project inside a Docker container.")
		t.note("Install it from docs.docker.com/engine/install, then start it.")
	}

	probePort := func(network, addr string) error {
		if network == "udp" {
			c, err := net.ListenPacket(network, addr)
			if err == nil {
				c.Close()
			}
			return err
		}
		l, err := net.Listen(network, addr)
		if err == nil {
			l.Close()
		}
		return err
	}
	ports := []struct{ network, addr, name string }{
		{"tcp", ":80", "80"}, {"tcp", ":443", "443"}, {"udp", ":53", "53"}, {"tcp", ":53", "53"},
	}
	var busy, denied []string
	for _, p := range ports {
		err := probePort(p.network, p.addr)
		if err != nil && !errors.Is(err, syscall.EACCES) && p.name == "53" && net.ParseIP(ip) != nil {
			err = probePort(p.network, net.JoinHostPort(ip, "53"))
		}
		switch {
		case errors.Is(err, syscall.EACCES):
			denied = appendOnce(denied, p.name)
		case err != nil:
			busy = appendOnce(busy, p.name)
		}
	}
	switch {
	case len(denied) > 0:
		t.line(t.warn(), "Can't check ports 80, 443 and 53 without root. Run pierd init with sudo.")
	case len(busy) > 0:
		t.line(t.warn(), fmt.Sprintf("Port %s is in use by another program on this machine", joinAnd(busy)))
		t.note("Pier needs 80 and 443 for its sites, and 53 to answer the certificate check.")
	default:
		t.line(t.ok(), "Ports 80, 443 and 53 are free on this machine")
	}
	t.note("If your provider has a firewall, also open TCP 80 and 443, and UDP and TCP 53.")

	if free, mount, err := diskFree(data); err == nil {
		gb := free / 1_000_000_000
		if gb < 10 {
			t.line(t.warn(), fmt.Sprintf("Only %d GB free on %s. Builds and deployments need room; 20 GB is comfortable.", gb, mount))
		} else {
			t.line(t.ok(), fmt.Sprintf("%d GB free on %s", gb, mount))
		}
	}

	if runtime.GOOS == "linux" && swapTotal() == 0 {
		t.line(t.warn(), "No swap. A build that runs out of memory is stopped without a message.")
		if os.Geteuid() != 0 {
			t.note("Run pierd init as root to add some, or add it yourself.")
		} else if t.yes("Add 4 GB of swap?") {
			if err := addSwap(ctx); err != nil {
				t.line(t.bad(), "Couldn't add swap: "+err.Error())
			} else {
				t.line(t.ok(), "4 GB of swap added")
			}
		}
	}
}

// createOwner asks for the owner's email and password and makes the account.
func createOwner(ctx context.Context, t *tty, st *store.Store, email string) (store.User, bool) {
	t.heading("Your account   " + t.dim("the owner's: it can do everything, and invite others"))
	for {
		if email == "" {
			if !t.interactive {
				fmt.Fprintln(os.Stderr, "pierd: pass the owner's email with -email")
				return store.User{}, false
			}
			var err error
			if email, err = t.ask(t.field("Email", 10)); err != nil {
				return store.User{}, false
			}
		} else {
			t.printf("%s%s\n", t.field("Email", 10), email)
		}
		if auth.ValidEmail(email) {
			break
		}
		t.note("That doesn't look like an email address.")
		if !t.interactive {
			return store.User{}, false
		}
		email = ""
	}
	var password string
	for {
		pw, err := t.secret(t.field("Password", 10), fmt.Sprintf("%d characters or more", auth.MinPassword))
		if err != nil {
			fmt.Fprintln(os.Stderr, "pierd: no password given")
			return store.User{}, false
		}
		if err := auth.CheckPasswordRules(pw); err != nil {
			t.note("Too short: " + err.Error() + ".")
			if !t.interactive {
				return store.User{}, false
			}
			continue
		}
		if t.interactive {
			again, err := t.secret(t.field("Again", 10), "")
			if err != nil {
				return store.User{}, false
			}
			if again != pw {
				t.note("Those two don't match. Once more:")
				continue
			}
		}
		password = pw
		break
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pierd:", err)
		return store.User{}, false
	}
	id, err := st.CreateUser(ctx, email, auth.NameFromEmail(email), hash)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pierd: create the account:", err)
		return store.User{}, false
	}
	t.line(t.ok(), "Account created")
	u, err := st.UserByID(ctx, id)
	return u, err == nil
}

// chooseAddresses asks for Pier's address (a domain, or the IP) and the base
// domain project addresses go under.
func chooseAddresses(t *tty, ip, address, base string) (string, string, bool) {
	t.heading("Addresses")
	const w = 15
	for {
		if address == "" && t.interactive {
			t.note("A domain like pier.example.com, or press Enter to use https://" + ip)
			a, err := t.ask(t.field("Pier's address", w))
			if err != nil {
				return "", "", false
			}
			address = strings.ToLower(strings.TrimSuffix(a, "."))
			t.redraw(2, t.field("Pier's address", w)+orIP(address, ip)+"   "+t.dim(ipHint(address, ip)))
		} else if address != "" {
			t.printf("%s%s\n", t.field("Pier's address", w), orIP(address, ip))
		}
		if address == "" || address == "ip" || address == ip {
			address = ip
			break
		}
		if dnscheck.ValidDomain(address) {
			break
		}
		t.note("That isn't a domain name. Type one like pier.example.com, or press Enter.")
		if !t.interactive {
			return "", "", false
		}
		address = ""
	}

	suggest := ""
	if address != ip && strings.Count(address, ".") >= 2 {
		_, suggest, _ = strings.Cut(address, ".")
	}
	for {
		if base == "" && t.interactive {
			if suggest != "" {
				t.note("Projects get addresses under it, like blog." + suggest + ". Press Enter to use " + suggest + ".")
			} else {
				t.note("Projects get addresses under it, like blog.example.com. Press Enter to skip for now.")
			}
			b, err := t.ask(t.field("Base domain", w))
			if err != nil {
				return "", "", false
			}
			base = strings.ToLower(strings.TrimSuffix(b, "."))
			if base == "" {
				base = suggest
			}
			shown := base
			if shown == "" {
				shown = "none yet"
			}
			hint := ""
			if base != "" {
				hint = "projects get addresses like blog." + base
			}
			t.redraw(2, t.field("Base domain", w)+pad(shown, 22)+t.dim(hint))
		} else if base == "" {
			base = suggest
			t.printf("%s%s\n", t.field("Base domain", w), orNone(base))
		} else {
			t.printf("%s%s\n", t.field("Base domain", w), orNone(base))
		}
		if base == "none" {
			base = ""
		}
		if base == "" || dnscheck.ValidDomain(base) {
			break
		}
		t.note("That isn't a domain name.")
		if !t.interactive {
			return "", "", false
		}
		base = ""
	}
	return address, base, true
}

func orIP(address, ip string) string {
	if address == "" || address == "ip" {
		return ip
	}
	return address
}

func ipHint(address, ip string) string {
	if address == "" || address == "ip" || address == ip {
		return "the dashboard uses an IP certificate, renewed every few days"
	}
	return ""
}

func orNone(base string) string {
	if base == "" {
		return "none yet"
	}
	return base
}

// redraw replaces the last n lines with one line, so an answered question
// reads like the finished form.
func (t *tty) redraw(n int, line string) {
	if !t.color {
		return
	}
	t.printf("\x1b[%dA\r\x1b[J%s\n", n, line)
}

type record struct {
	Type, Host, Value string
	check             func(context.Context) dnscheck.Result
}

// records lists what the owner adds at their DNS provider: Pier's own
// address, every project address (*), and the NS record that hands the
// certificate check for the base domain to Pier.
func records(ip, address, base string) []record {
	var rs []record
	local := func(d string) bool { return d == "localhost" || strings.HasSuffix(d, ".localhost") }
	if local(address) {
		address = ip // *.localhost resolves on this machine: nothing to add
	}
	if local(base) {
		base = ""
	}
	rel := func(name string) string {
		if base != "" && name == base {
			return "@"
		}
		if base != "" && strings.HasSuffix(name, "."+base) {
			return strings.TrimSuffix(name, "."+base)
		}
		return name
	}
	nsTarget := address
	if (address == ip || address == base) && base != "" {
		nsTarget = "pier." + base
	}
	if address != ip && address != nsTarget {
		host := address
		rs = append(rs, record{"A", rel(host), ip, func(ctx context.Context) dnscheck.Result { return dnscheck.CheckA(ctx, host, ip) }})
	}
	if nsTarget != ip {
		host := nsTarget
		rs = append(rs, record{"A", rel(host), ip, func(ctx context.Context) dnscheck.Result { return dnscheck.CheckA(ctx, host, ip) }})
	}
	if base != "" {
		rs = append(rs, record{"A", "*", ip, func(ctx context.Context) dnscheck.Result { return dnscheck.CheckWildcard(ctx, base, ip) }})
		name := "_acme-challenge." + base
		rs = append(rs, record{"NS", "_acme-challenge", nsTarget, func(ctx context.Context) dnscheck.Result {
			return dnscheck.CheckDelegation(ctx, name, base, nsTarget)
		}})
	}
	return rs
}

// checkRecords shows the records and checks them every 10 seconds, until
// they're all found or the owner presses Enter.
func checkRecords(ctx context.Context, t *tty, rs []record) {
	if len(rs) == 0 {
		return
	}
	t.heading("Add these records at your DNS provider")
	t.println()
	hw, vw := 18, 24
	for _, r := range rs {
		hw, vw = max(hw, len(r.Host)+2), max(vw, len(r.Value)+2)
	}
	t.printf("    %s\n", t.dim(pad("Type", 6)+pad("Host", hw)+"Value"))
	var notes []string
	for _, r := range rs {
		if r.Type == "NS" {
			notes = append(notes, "The NS record lets Pier get one certificate that covers every project address.")
		}
	}
	if t.interactive {
		notes = append(notes, "DNS can take a few minutes. Leave this running, or press Enter and Pier keeps checking.")
	}

	results := make([]dnscheck.Result, len(rs))
	draw := func(first bool) {
		if !first {
			t.printf("\x1b[%dA", len(rs)+1+len(notes))
		}
		for i, r := range rs {
			t.printf("\r\x1b[2K    %s%s%s%s\n", pad(r.Type, 6), pad(r.Host, hw), pad(r.Value, vw), t.status(results[i]))
		}
		t.printf("\r\x1b[2K\n")
		for _, n := range notes {
			t.printf("\r\x1b[2K    %s\n", t.dim(n))
		}
	}
	checkAll := func() bool {
		all := true
		for i, r := range rs {
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			results[i] = r.check(cctx)
			cancel()
			all = all && results[i].Found
		}
		return all
	}
	done := checkAll()
	draw(true)
	if done || !t.interactive || !t.color {
		return
	}
	enter := make(chan struct{}, 1)
	go func() {
		t.in.ReadString('\n')
		enter <- struct{}{}
	}()
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-enter:
			return
		case <-tick.C:
			done = checkAll()
			draw(false)
			if done {
				return
			}
		}
	}
}

func (t *tty) status(r dnscheck.Result) string {
	switch {
	case r.Found:
		return t.ok() + " found"
	case r.Got != "":
		return t.bad() + " points to " + r.Got
	default:
		return t.waiting() + " checking every 10 seconds"
	}
}

// startService restarts pierd under systemd when the install script set it
// up, so it picks up the new settings; otherwise it says how to start it.
func startService(t *tty, data string) {
	if _, err := exec.LookPath("systemctl"); err == nil {
		if exec.Command("systemctl", "is-enabled", "--quiet", "pierd").Run() == nil {
			if err := exec.Command("systemctl", "restart", "pierd").Run(); err == nil {
				t.line(t.ok(), "pierd is running, and starts again on boot")
				return
			}
		}
	}
	t.line(t.waiting(), "Start pierd, or restart it so it uses these addresses:  pierd serve -data "+data)
}

// resetPassword is pierd reset-password: a new password for one account,
// the owner's unless -email names another, typed on the server. It also signs
// that person out of every browser and computer. Pier never sends email, so
// this is the way back in.
func resetPassword(args []string) int {
	fs := flag.NewFlagSet("reset-password", flag.ExitOnError)
	data := dataFlag(fs)
	email := fs.String("email", "", "whose password to reset; the owner's if left out")
	pwStdin := fs.Bool("password-stdin", false, "read the new password from stdin, for scripts")
	fs.Parse(args)
	t := newTTY()
	if *pwStdin {
		t.interactive = false
	}
	ctx := context.Background()
	defer chownDB(*data)
	st, err := store.Open(layout(*data).db)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pierd: open the database:", err)
		return 1
	}
	defer st.Close()
	var who store.User
	if *email != "" {
		who, err = st.UserByEmail(ctx, strings.TrimSpace(*email))
		if errors.Is(err, store.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "pierd: there's no account for %s here\n", *email)
			return 1
		}
	} else {
		who, err = st.Owner(ctx)
		if errors.Is(err, store.ErrNotFound) {
			fmt.Fprintln(os.Stderr, "pierd: nobody has an account here yet; run pierd init")
			return 1
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pierd:", err)
		return 1
	}
	t.printf("\nA new password for %s\n", who.Email)
	for {
		pw, err := t.secret(t.field("Password", 10), fmt.Sprintf("%d characters or more", auth.MinPassword))
		if err != nil {
			return 1
		}
		if err := auth.CheckPasswordRules(pw); err != nil {
			t.note("Too short: " + err.Error() + ".")
			if !t.interactive {
				return 1
			}
			continue
		}
		if t.interactive {
			again, err := t.secret(t.field("Again", 10), "")
			if err != nil {
				return 1
			}
			if again != pw {
				t.note("Those two don't match. Once more:")
				continue
			}
		}
		hash, err := auth.HashPassword(pw)
		if err == nil {
			err = st.SetPassword(ctx, who.ID, hash)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "pierd:", err)
			return 1
		}
		break
	}
	n, err := st.DeleteSessions(ctx, who.ID, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "pierd: sign out the old sessions:", err)
		return 1
	}
	t.line(t.ok(), "Password changed")
	if n > 0 {
		t.note(fmt.Sprintf("Signed out of %s. Sign in again with the new password.", plural(int(n), "browser or computer", "browsers and computers")))
	}
	return 0
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func appendOnce(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func joinAnd(s []string) string {
	switch len(s) {
	case 0:
		return ""
	case 1:
		return s[0]
	default:
		return strings.Join(s[:len(s)-1], ", ") + " and " + s[len(s)-1]
	}
}

// diskFree reports the free space where Pier keeps its data, and the mount
// point it's on.
func diskFree(path string) (uint64, string, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return 0, "", err
	}
	return s.Bavail * uint64(s.Bsize), mountPoint(path), nil
}

// mountPoint walks up from path while it stays on the same device.
func mountPoint(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	dev := func(p string) (uint64, bool) {
		fi, err := os.Stat(p)
		if err != nil {
			return 0, false
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return 0, false
		}
		return uint64(st.Dev), true
	}
	d, ok := dev(abs)
	if !ok {
		return abs
	}
	for abs != "/" {
		parent := filepath.Dir(abs)
		pd, ok := dev(parent)
		if !ok || pd != d {
			return abs
		}
		abs = parent
	}
	return "/"
}

func swapTotal() int64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "SwapTotal:"); ok {
			var kb int64
			fmt.Sscanf(strings.TrimSpace(rest), "%d", &kb)
			return kb
		}
	}
	return -1
}

// addSwap makes a 4 GB swap file and turns it on now and at every boot.
func addSwap(ctx context.Context) error {
	if _, err := os.Stat("/swapfile"); err == nil {
		return errors.New("/swapfile already exists")
	}
	for _, c := range [][]string{
		{"fallocate", "-l", "4G", "/swapfile"},
		{"chmod", "600", "/swapfile"},
		{"mkswap", "/swapfile"},
		{"swapon", "/swapfile"},
	} {
		if out, err := exec.CommandContext(ctx, c[0], c[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %s", strings.Join(c, " "), strings.TrimSpace(string(out)))
		}
	}
	fstab, err := os.ReadFile("/etc/fstab")
	if err != nil {
		return err
	}
	if !strings.Contains(string(fstab), "/swapfile") {
		f, err := os.OpenFile("/etc/fstab", os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.WriteString("/swapfile none swap sw 0 0\n"); err != nil {
			return err
		}
	}
	return nil
}

func chownDB(data string) {
	if os.Geteuid() != 0 {
		return
	}
	uid, gid := dirOwner(data)
	if uid == 0 && gid == 0 {
		return
	}
	db := layout(data).db
	for _, p := range []string{db, db + "-wal", db + "-shm"} {
		_ = os.Chown(p, uid, gid)
	}
}

func dirOwner(path string) (int, int) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), int(st.Gid)
	}
	return 0, 0
}
