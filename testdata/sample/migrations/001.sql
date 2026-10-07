CREATE TABLE records (body TEXT);
CREATE TABLE audit_log (body TEXT);
CREATE TABLE relation (parent_id TEXT REFERENCES records(body));
CREATE INDEX record_body ON records(body);
CREATE TRIGGER record_change AFTER INSERT ON records BEGIN
  INSERT INTO audit_log VALUES (NEW.body);
END;
