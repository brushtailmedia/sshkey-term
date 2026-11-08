//go:build e2e

package e2e

import "strings"

// serverTOML is the server config every test starts from: the shape of the
// server repo's test-fixture config with the values a harness needs —
// loopback on port 0, generous limits so the flows never trip them by
// accident, every scheduler off, fast shutdown, JSON logs at info. A test
// that needs one knob changed passes a withConfig override.
const serverTOML = `# written by sshkey-term/internal/e2e
[server]
port = 0
bind = "127.0.0.1"
allow_self_leave_rooms = false
allow_self_leave_retired_rooms = true

[server.auto_revoke]
enabled = false
prune_after_hours = 168

[server.quotas.user]
enabled = false
allow_exempt_users = false
daily_upload_bytes_warn = "1GB"
daily_upload_bytes_block = "5GB"
flag_consecutive_days = 2
retention_days = 30

[messages]
max_body_size = "16KB"

[retention]
purge_days = 0

[sync]
window_messages = 200
window_days = 7
history_page_size = 100

[files]
max_file_size = "100MB"
max_avatar_size = "256KB"
allowed_avatar_types = ["image/png", "image/jpeg"]
max_file_ids_per_message = 20

[devices]
max_per_user = 10

[groups]
max_members = 100

[rooms]
typing_max_members = 100

[rate_limits]
messages_per_second = 200
uploads_per_minute = 6000
connections_per_minute = 600
failed_auth_per_minute = 600
typing_per_second = 60
history_per_minute = 600
deletes_per_minute = 100
admin_deletes_per_minute = 500
reactions_per_minute = 300
dm_creates_per_minute = 50
profiles_per_minute = 50
pins_per_minute = 100
admin_actions_per_minute = 200
edits_per_minute = 100
room_members_per_minute = 60
device_list_per_minute = 60
download_requests_per_minute = 600
error_responses_per_minute = 600
idle_timeout_seconds = 0
per_client_write_buffer_size = 256
consecutive_drop_disconnect_threshold = 10

[shutdown]
grace_period = "100ms"

[logging]
level = "info"
file = ""
max_size_mb = 100
max_files = 5
format = "json"

[backup]
enabled = false
interval = "24h"
dest_dir = "backups"
retention_count = 10
retention_age = "720h"
compress = true
skip_if_idle = true
include_config_files = true

[push.apns]
enabled = false

[push.fcm]
enabled = false
`

// withConfig edits the template before it is written: one exact line in,
// one exact line out, and the test fails if the line is not there (a
// silent no-op would test the wrong server).
func withConfig(oldLine, newLine string) envOption {
	return func(o *envOptions) {
		prev := o.configOverride
		o.configOverride = func(toml string) string {
			if prev != nil {
				toml = prev(toml)
			}
			if !strings.Contains(toml, oldLine+"\n") {
				panic("withConfig: template has no line " + oldLine)
			}
			return strings.Replace(toml, oldLine+"\n", newLine+"\n", 1)
		}
	}
}
