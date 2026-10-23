-- Backfill release image references for rows created before release identity
-- existed, and stop treating the synthetic finished_at value from 0004 as a
-- measured deploy duration.
UPDATE releases SET image = CASE
    WHEN digest != '' THEN repository || '@' || digest
    WHEN tag != '' THEN repository || ':' || tag
    ELSE repository
  END
  WHERE image = '' AND repository != '';

UPDATE releases SET finished_at = NULL
  WHERE config_version = 0 AND finished_at = created_at;
