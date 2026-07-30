//go:build windows

package claude

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestMergeUserConfigAppliesRestrictiveDACL pins the Windows equivalent of
// 0600 on ~/.claude.json: a protected DACL whose only trustees are the file
// owner, SYSTEM and Administrators. Everyone/Users must never appear.
func TestMergeUserConfigAppliesRestrictiveDACL(t *testing.T) {
	home := t.TempDir()
	if _, _, err := MergeUserConfig(home, []byte(`{"mcpServers":{"context7":{"command":"npx"}}}`)); err != nil {
		t.Fatalf("MergeUserConfig() error = %v", err)
	}

	sd, err := windows.GetNamedSecurityInfo(UserConfigPath(home), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo() error = %v", err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatalf("Control() error = %v", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("DACL is not protected: inherited ACEs can widen access")
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("DACL() = %v, %v; want a non-nil DACL", dacl, err)
	}

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser() error = %v", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		user.User.Sid.String(): true,
		system.String():        true,
		admins.String():        true,
	}

	// x/sys does not export the ACL header fields; read AceCount through the
	// documented Win32 ACL layout (revision, sbz1, size, count, sbz2).
	type win32ACLHeader struct {
		AclRevision byte
		Sbz1        byte
		AclSize     uint16
		AceCount    uint16
		Sbz2        uint16
	}
	aceCount := (*win32ACLHeader)(unsafe.Pointer(dacl)).AceCount
	if aceCount == 0 {
		t.Fatal("DACL has no ACEs: the file would be inaccessible even to its owner")
	}
	for i := uint32(0); i < uint32(aceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatalf("GetAce(%d) error = %v", i, err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !allowed[sid.String()] {
			t.Fatalf("DACL grants access to unexpected trustee %s", sid.String())
		}
	}
}
