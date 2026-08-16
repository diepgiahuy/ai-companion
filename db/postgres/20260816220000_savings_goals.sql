CREATE TABLE savings_goals (
    user_id text NOT NULL DEFAULT '',
    period text NOT NULL,
    target_vnd bigint NOT NULL CHECK(target_vnd > 0 AND target_vnd <= 1000000000000),
    description text NOT NULL DEFAULT '',
    effective_from timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(user_id, period)
);

CREATE OR REPLACE FUNCTION companion_outbox_savings_goal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO outbox(event_id, source, event_type, subject, user_id, data_json, occurred_at, next_attempt_at)
    VALUES(gen_random_uuid()::text, '/companion/domain', 'savings_goal.updated', 'savings_goal/'||NEW.period, NEW.user_id, jsonb_build_object('period', NEW.period, 'target_vnd', NEW.target_vnd), clock_timestamp(), clock_timestamp());
    RETURN NEW;
END
$$;

CREATE OR REPLACE FUNCTION companion_outbox_savings_goal_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO outbox(event_id, source, event_type, subject, user_id, data_json, occurred_at, next_attempt_at)
    VALUES(gen_random_uuid()::text, '/companion/domain', 'savings_goal.deleted', 'savings_goal/'||OLD.period, OLD.user_id, jsonb_build_object('period', OLD.period, 'target_vnd', OLD.target_vnd), clock_timestamp(), clock_timestamp());
    RETURN OLD;
END
$$;

CREATE TRIGGER trg_savings_goals_ai AFTER INSERT ON savings_goals FOR EACH ROW EXECUTE FUNCTION companion_outbox_savings_goal();
CREATE TRIGGER trg_savings_goals_au AFTER UPDATE ON savings_goals FOR EACH ROW EXECUTE FUNCTION companion_outbox_savings_goal();
CREATE TRIGGER trg_savings_goals_ad AFTER DELETE ON savings_goals FOR EACH ROW EXECUTE FUNCTION companion_outbox_savings_goal_delete();
