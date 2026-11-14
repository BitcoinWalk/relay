package deploy_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestProductionMembershipInstallerGuards(t *testing.T) {
	name := "install-chat-membership-production.sh"
	for _, args := range [][]string{{}, {"--staging"}, {"--apply", "extra"}} {
		output, err := exec.Command("bash", append([]string{name}, args...)...).CombinedOutput()
		if err == nil || !strings.Contains(string(output), "Run with sudo") {
			t.Fatalf("unsafe arguments accepted: %v: %s", args, output)
		}
	}
	if output, err := exec.Command("bash", "-n", name).CombinedOutput(); err != nil {
		t.Fatalf("syntax: %s %v", output, err)
	}
	data, err := os.ReadFile(name)
	if err != nil { t.Fatal(err) }
	script := string(data)
	for _, guard := range []string{"db5efeee38bb9199b0ed906ea82bc096c2c57dc1941df0342f1d43d10d4c4797", "e8bf5c1080f361a278a7e3e16b58c4ff434b9deb95210e916b4eb42bc857bca1", "DropInPaths", "persistent-chat-production", "[[ $status == 401 ]]", "flock -n 9"} {
		if !strings.Contains(script, guard) { t.Fatalf("missing guard %s", guard) }
	}
	stop := strings.Index(script, "systemctl stop \"$service\"\ncp --reflink")
	backup := strings.Index(script, "cp --reflink=auto \"$database\" \"$backup/chat.db\"")
	install := strings.Index(script, "install -o root -g root -m 0755 \"$backup/bitcoinwalk-relay\"")
	if stop < 0 || backup <= stop || install <= backup { t.Fatal("offline backup must precede activation") }
}

func TestChatMembershipInstallerRequiresExplicitApply(t *testing.T) {
	for _, args := range [][]string{{}, {"--production"}, {"--apply", "extra"}} {
		command := exec.Command("bash", append([]string{"install-chat-membership-staging.sh"}, args...)...)
		output, err := command.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "Usage: sudo bash") {
			t.Fatalf("arguments %v were not rejected: %s %v", args, output, err)
		}
	}
	if output, err := exec.Command("bash", "-n", "install-chat-membership-staging.sh").CombinedOutput(); err != nil {
		t.Fatalf("shell syntax: %s %v", output, err)
	}
}
