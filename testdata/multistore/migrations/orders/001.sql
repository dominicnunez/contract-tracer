CREATE TABLE records(id INTEGER);
CREATE TABLE audit_log(record_id INTEGER REFERENCES records(id));
CREATE TRIGGER record_audit AFTER INSERT ON records BEGIN
  INSERT INTO audit_log(record_id) VALUES (NEW.id);
END;
