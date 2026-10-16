package completion

import "testing"

const queuedMs = `{"op_id": "20260301T120000.000000-1-aaaa", "ts": "2026-03-01T12:00:00", "op": "ms_new", "ref": "erp#M-1", "args": {"title": "Local One", "description": "", "due": "", "local_id": "M-1"}, "attempts": 0, "last_error": null}` + "\n"

func TestMilestoneWriteCommands(t *testing.T) {
	f := newFixture(t, standard)
	f.write("outbox/20260301T120000.000000-1-aaaa.json", queuedMs)
	want(t, "subcommands", list(f.complete("issues", "milestones", "")), "show new edit close reopen")
	want(t, "subcommand prefix", list(f.complete("issues", "milestones", "re")), "reopen")
	want(t, "edit offers titles and local ids", list(f.complete("issues", "milestones", "edit", "-R", "erp", "")), "Local One M-1 old v1 v1 beta")
	want(t, "close offers open ones only", list(f.complete("issues", "milestones", "close", "-R", "erp", "")), "Local One M-1 v1 v1 beta")
	want(t, "reopen offers closed ones only", list(f.complete("issues", "milestones", "reopen", "-R", "erp", "")), "old")
	want(t, "id prefix", list(f.complete("issues", "milestones", "edit", "-R", "erp", "M")), "M-1")
	want(t, "-m offers local ones too", list(f.complete("issues", "ls", "-R", "erp", "-m", "")), "Local One M-1 old v1 v1 beta")
	want(t, "new -m", list(f.complete("issues", "new", "--repo", "erp", "--title", "t", "-m", "L")), "Local One")
	want(t, "edit -d", list(f.complete("issues", "milestones", "edit", "x", "-d", "")), "none")
	want(t, "new -d offers nothing", list(f.complete("issues", "milestones", "new", "x", "-d", "")), "")
	want(t, "edit flags", list(f.complete("issues", "milestones", "edit", "x", "--")), "--repo --title --due --description --description-file --all --help --config --dir")
}
