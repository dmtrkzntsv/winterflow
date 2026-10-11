-- Organizations get a visual identity (icon + color, same vocabulary as apps).

ALTER TABLE organizations ADD COLUMN icon VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE organizations ADD COLUMN color VARCHAR(7) NOT NULL DEFAULT '';
