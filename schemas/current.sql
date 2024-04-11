CREATE TABLE config(
	name STRING PRIMARY KEY,
	value
);

CREATE TABLE users(
	name STRING PRIMARY KEY,
	passwd STRING,
	salt STRING,
	role STRING
);

CREATE TABLE registries(
	id INTEGER PRIMARY KEY,
	name STRING,
	url STRING,
	user STRING,
	password STRING,
	timeout INTEGER
); 

CREATE TABLE projects(
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

CREATE TABLE destinations(
	project INTEGER,
	registry INTEGER,
	tag STRING
);

CREATE TABLE tasks(
	id INTEGER PRIMARY KEY,
	project INTEGER,
	type STRING,
	state STRING,
	time STRING
);

CREATE TABLE members(
	project INTEGER,
	user STRING,
	role STRING
);

CREATE TABLE triggers(
	project INTEGER,
	target INTEGER,
	state STRING
);

CREATE TABLE credentials(
	id INTEGER PRIMARY KEY,
	description STRING,
	value STRING
);

CREATE TABLE environments(
	project INTEGER,
	name STRING,
	credential INTEGER
);

INSERT INTO config(name, value) VALUES('version', 3);