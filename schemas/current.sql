CREATE TABLE IF NOT EXISTS config(
	name STRING PRIMARY KEY,
	value
);

CREATE TABLE IF NOT EXISTS users(
	name STRING PRIMARY KEY,
	passwd STRING,
	salt STRING,
	role STRING
);

CREATE TABLE IF NOT EXISTS registries(
	name STRING PRIMARY KEY,
	url STRING,
	user STRING,
	password STRING,
	timeout INTEGER
); 

CREATE TABLE IF NOT EXISTS projects(
	id INTEGER PRIMARY KEY,
	name STRING,
	source STRING,
	branch STRING,
	destination STRING,
	tag STRING,
	buildSpec STRING,
	prepackageSpec STRING,
	packageSpec STRING,
	state STRING,
	version INTEGER,
	buildHash BLOB,
	labels STRING,
	protected INTEGER,
	tagRepo INTEGER
);

CREATE TABLE IF NOT EXISTS tasks(
	id INTEGER PRIMARY KEY,
	project INTEGER,
	type STRING,
	state STRING,
	time STRING
);

CREATE TABLE IF NOT EXISTS members(
	project INTEGER,
	user STRING,
	role STRING
);

CREATE TABLE IF NOT EXISTS triggers(
	project INTEGER,
	target INTEGER,
	state STRING
);

CREATE TABLE IF NOT EXISTS credentials(
	id INTEGER PRIMARY KEY,
	description STRING,
	value STRING
);

CREATE TABLE IF NOT EXISTS environments(
	project INTEGER,
	name STRING,
	credential INTEGER
);

INSERT INTO config(name, value) VALUES('version', 1);