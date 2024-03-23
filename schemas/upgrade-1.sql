ALTER TABLE registries RENAME TO registries_old;

CREATE TABLE registries(
	id INTEGER PRIMARY KEY,
	name STRING,
	url STRING,
	user STRING,
	password STRING,
	timeout INTEGER
);

INSERT INTO registries(name, url, user, password, timeout) SELECT
	name, url, user, password, timeout
FROM
	registries_old;

DROP TABLE registries_old;

CREATE TABLE destinations(
	project INTEGER,
	registry INTEGER,
	tag STRING
);

INSERT INTO destinations(project, registry, tag) SELECT
	p.id, r.id, p.tag
FROM
	projects p, registries r
WHERE
	p.destination = r.name;


ALTER TABLE projects DROP COLUMN destination;
ALTER TABLE projects DROP COLUMN tag;
