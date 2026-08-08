-- Export readiness rehearsal (BACKEND.md Phase 8, exit criterion 6).
--
-- **Nothing here ships.** The v0.1 CSV export is deferred (D13, §6.5) and its
-- shape is a design question best answered with real data in front of it. What
-- is not deferrable is whether the record can answer these questions at all —
-- and that can only be fixed while the product is still cheap to change, not in
-- v0.1 when the missing months cannot be recovered.
--
-- Run against a real project:
--
--   psql zettaplanner -v pid="'<project-uuid>'" -f docs/export-rehearsal.sql
--
-- Three queries, one per thing a retrospective asks. Read the findings in
-- BACKEND.md's Phase 8 notes before trusting the output.

\set ON_ERROR_STOP on
\timing off

\echo ''
\echo '== (a) every status transition, with how long the item sat in the state it left =='
\echo ''

-- Durations are DERIVED, never stored (§6.5 constraint 4). Given complete
-- transitions with timestamps, "how long was ZP-14 blocked" is a window
-- function; a stored duration column is a second source of truth that
-- disagrees with the log under exactly the conditions nobody tests.
--
-- `lag` over the item's own transitions gives the moment it entered the state
-- it is now leaving. The first transition of an item's life has no predecessor,
-- so its duration is measured from the row's creation instead — which is what
-- makes "how long did anything sit in todo" answerable at all.
WITH transitions AS (
  SELECT a.subject_id                             AS item_id,
         coalesce(w.ref, '(deleted)')             AS ref,
         coalesce(w.title, '')                    AS title,
         a.before ->> 'status'                    AS from_status,
         a.after  ->> 'status'                    AS to_status,
         a.at,
         coalesce(u.display_name, ac.agent_name, ac.kind) AS actor,
         ac.kind                                  AS actor_kind,
         w.created_at                             AS item_created_at,
         lag(a.at) OVER (PARTITION BY a.subject_id ORDER BY a.at) AS entered_at
    FROM audit_log a
    JOIN actors ac ON ac.id = a.actor_id
    LEFT JOIN users u ON u.id = ac.user_id
    LEFT JOIN work_items w ON w.id = a.subject_id
   WHERE a.project_id = :pid
     AND a.action = 'work_item.status_changed'
)
SELECT ref,
       left(title, 40)                                        AS title,
       from_status || ' -> ' || to_status                      AS moved,
       to_char(at, 'YYYY-MM-DD HH24:MI')                       AS at,
       actor,
       actor_kind,
       justify_interval(at - coalesce(entered_at, item_created_at)) AS spent_in_previous_state
  FROM transitions
 ORDER BY ref, at;

\echo ''
\echo '== (a2) total time per status per item — the bottleneck question =='
\echo ''

WITH transitions AS (
  SELECT a.subject_id AS item_id,
         a.before ->> 'status' AS from_status,
         a.at,
         lag(a.at) OVER (PARTITION BY a.subject_id ORDER BY a.at) AS entered_at,
         w.created_at
    FROM audit_log a
    LEFT JOIN work_items w ON w.id = a.subject_id
   WHERE a.project_id = :pid
     AND a.action = 'work_item.status_changed'
)
SELECT coalesce(w.ref, '(deleted)') AS ref,
       t.from_status                AS status,
       justify_interval(sum(t.at - coalesce(t.entered_at, t.created_at))) AS total_time,
       count(*)                     AS times_entered
  FROM transitions t
  LEFT JOIN work_items w ON w.id = t.item_id
 GROUP BY w.ref, t.from_status
 ORDER BY sum(t.at - coalesce(t.entered_at, t.created_at)) DESC;

\echo ''
\echo '== (a3) how long each open item has been in its CURRENT state, right now =='
\echo ''

-- Found by the Phase 8 rehearsal, and the query the other two do not cover.
--
-- (a) and (a2) measure *closed* intervals: a state is timed by the transition
-- that leaves it. An item that has never moved therefore appears in neither —
-- and "ZP-4 has been blocked since the day it was created and nobody has
-- touched it" is precisely the thing a retrospective is looking for. The
-- entry into the current state is `max(at)` over its transitions, or the row's
-- creation when there are none.
--
-- Nothing is stored to make this work; it is the log plus now(). But it does
-- mean the v0.1 export cannot be transitions alone — it needs the current row
-- beside them, or the longest-running problems are the ones it omits.
SELECT w.ref,
       left(w.title, 40)                                  AS title,
       w.status                                           AS current_status,
       coalesce(u.display_name, '(unassigned)')           AS assignee,
       justify_interval(now() - coalesce(t.entered_at, w.created_at)) AS in_this_state_for,
       CASE WHEN t.entered_at IS NULL THEN 'never moved' ELSE '' END AS note
  FROM work_items w
  LEFT JOIN users u ON u.id = w.assignee_id
  LEFT JOIN LATERAL (
    SELECT max(a.at) AS entered_at
      FROM audit_log a
     WHERE a.subject_id = w.id AND a.action = 'work_item.status_changed'
  ) t ON true
 WHERE w.project_id = :pid
   AND w.deleted_at IS NULL
   AND w.status NOT IN ('done', 'dropped')
 ORDER BY coalesce(t.entered_at, w.created_at);

\echo ''
\echo '== (b) every worklog and deviation, with actor and timestamp =='
\echo ''

-- One list rather than two. A worklog and the deviation raised from it are the
-- same afternoon, and reading them in separate files means reconstructing the
-- order by eye. `source` is what a CSV would carry as a column.
--
-- Worklogs are never edited or deleted (§6.5 constraint 1) — a correction is
-- another worklog — so this list is complete by construction rather than by
-- filtering.
SELECT 'worklog'                                  AS source,
       to_char(wl.created_at, 'YYYY-MM-DD HH24:MI') AS at,
       coalesce(u.display_name, ac.agent_name, ac.kind) AS actor,
       ac.kind                                    AS actor_kind,
       coalesce(w.ref, '(project)')               AS item,
       wl.kind                                    AS kind_or_severity,
       NULL                                       AS resolved_at,
       left(wl.body, 90)                          AS body
  FROM worklogs wl
  JOIN actors ac ON ac.id = wl.actor_id
  LEFT JOIN users u ON u.id = ac.user_id
  LEFT JOIN work_items w ON w.id = wl.work_item_id
 WHERE wl.project_id = :pid

UNION ALL

SELECT 'deviation',
       to_char(d.created_at, 'YYYY-MM-DD HH24:MI'),
       coalesce(u.display_name, ac.agent_name, ac.kind),
       ac.kind,
       coalesce(w.ref, '(project)'),
       d.severity,
       to_char(d.resolved_at, 'YYYY-MM-DD HH24:MI'),
       left(d.summary, 90)
  FROM deviations d
  JOIN actors ac ON ac.id = d.actor_id
  LEFT JOIN users u ON u.id = ac.user_id
  LEFT JOIN work_items w ON w.id = d.work_item_id
 WHERE d.project_id = :pid
   AND d.deleted_at IS NULL

 ORDER BY at, source;

\echo ''
\echo '== (c) agent turns: tokens, duration, error class =='
\echo ''

-- The measurement set BACKEND.md §4 asks for. `error_class` is the column that
-- makes "is one retry enough for tool_parse" (Q4) an aggregate rather than a
-- reading exercise over logs that have since rotated.
SELECT to_char(t.started_at, 'YYYY-MM-DD HH24:MI') AS started,
       u.display_name                              AS requested_by,
       t.status,
       coalesce(t.model, '(none)')                 AS model,
       t.turns                                     AS model_calls,
       t.tokens_in,
       t.tokens_out,
       coalesce(t.error_class, '')                 AS error_class,
       round(extract(epoch FROM (t.ended_at - t.started_at))::numeric, 1) AS seconds,
       (SELECT count(*) FROM proposals p WHERE p.turn_id = t.id) AS proposals_raised
  FROM agent_turns t
  JOIN users u ON u.id = t.requested_by
 WHERE t.project_id = :pid
 ORDER BY t.started_at;

\echo ''
\echo '== (c2) the same, aggregated — what §4 actually tracks =='
\echo ''

SELECT coalesce(model, '(none)')                        AS model,
       count(*)                                         AS turns,
       count(*) FILTER (WHERE status = 'complete')      AS completed,
       count(*) FILTER (WHERE status = 'failed')        AS failed,
       count(*) FILTER (WHERE error_class IS NOT NULL)  AS with_error_class,
       round(avg(tokens_in))                            AS avg_tokens_in,
       round(avg(tokens_out))                           AS avg_tokens_out,
       round(avg(turns), 2)                             AS avg_model_calls,
       round(avg(extract(epoch FROM (ended_at - started_at)))::numeric, 1) AS avg_seconds
  FROM agent_turns
 WHERE project_id = :pid
 GROUP BY model
 ORDER BY turns DESC;

\echo ''
\echo '== (d) coverage: does the log actually carry what the export needs =='
\echo ''

-- Not one of the three, and the most useful query here. A retrospective that
-- silently omits an action class is worse than one that refuses to run, so this
-- is the query to look at first: an action with zero rows in a project where it
-- obviously happened is a write path that is not recording.
SELECT action,
       count(*)                                     AS entries,
       count(DISTINCT actor_id)                     AS actors,
       count(*) FILTER (WHERE before IS NULL)       AS without_before,
       count(*) FILTER (WHERE after IS NULL)        AS without_after,
       count(*) FILTER (WHERE on_behalf_of IS NOT NULL) AS agent_on_behalf,
       count(DISTINCT request_id)                   AS operations,
       to_char(min(at), 'YYYY-MM-DD')               AS first_seen,
       to_char(max(at), 'YYYY-MM-DD')               AS last_seen
  FROM audit_log
 WHERE project_id = :pid
 GROUP BY action
 ORDER BY action;
