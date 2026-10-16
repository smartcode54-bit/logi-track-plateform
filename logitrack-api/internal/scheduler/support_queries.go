package scheduler

import _ "embed"

// Embed canonical sqlc query sources until P1.0 regenerates the shared query packages.
// The statements run through the caller's existing authorized transaction.

//go:embed queries/support_list_visiting_users.sql
var supportListVisitingUsersSQL string

//go:embed queries/support_lock_visit_sessions.sql
var supportLockVisitSessionsSQL string

//go:embed queries/support_lock_visit_user.sql
var supportLockVisitUserSQL string
