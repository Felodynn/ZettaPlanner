-- Reverting 0001 drops the audit log, whose stated retention is never
-- (CONTRACT.md §6.5 constraint 5). This file exists so development databases
-- are disposable. Nothing in a deployment should ever run it.

DROP TRIGGER IF EXISTS messages_no_truncate ON messages;
DROP TRIGGER IF EXISTS messages_no_delete ON messages;
DROP TRIGGER IF EXISTS worklogs_no_truncate ON worklogs;
DROP TRIGGER IF EXISTS worklogs_no_delete ON worklogs;
DROP TRIGGER IF EXISTS audit_log_no_truncate ON audit_log;
DROP TRIGGER IF EXISTS audit_log_no_delete ON audit_log;
DROP TRIGGER IF EXISTS audit_log_no_update ON audit_log;
DROP FUNCTION IF EXISTS zp_append_only();

DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS chunks;
DROP TABLE IF EXISTS project_digest;
DROP TABLE IF EXISTS notes;
DROP TABLE IF EXISTS proposal_changes;
DROP TABLE IF EXISTS proposals;
DROP TABLE IF EXISTS agent_turns;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS threads;
DROP TABLE IF EXISTS deviations;
DROP TABLE IF EXISTS worklogs;
DROP TABLE IF EXISTS checklist_items;
DROP TABLE IF EXISTS item_entity_refs;
DROP TABLE IF EXISTS item_dependencies;
DROP TABLE IF EXISTS sub_tasks;
DROP TABLE IF EXISTS work_items;
DROP TABLE IF EXISTS milestones;
DROP TABLE IF EXISTS ingest_pages;
DROP TABLE IF EXISTS ingests;
DROP TABLE IF EXISTS diagram_leases;
DROP TABLE IF EXISTS diagram_revisions;
DROP TABLE IF EXISTS diagram_edge_layout;
DROP TABLE IF EXISTS diagram_layout;
DROP TABLE IF EXISTS diagram_edges;
DROP TABLE IF EXISTS diagram_nodes;
DROP TABLE IF EXISTS diagrams;
DROP TABLE IF EXISTS catalog_entities;
DROP TABLE IF EXISTS project_members;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS actors;
DROP TABLE IF EXISTS users;
