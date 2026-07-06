CREATE TABLE db_activity (
    day varchar(20) PRIMARY KEY,
    uploaded boolean NOT NULL,
    tdata json NOT NULL,
    tzname varchar(50) NOT NULL,
    tzoffset int NOT NULL,
    clientversion varchar(20) NOT NULL,
    clientarch varchar(20) NOT NULL,
    buildtime varchar(20) NOT NULL DEFAULT '-',
    osrelease varchar(20) NOT NULL DEFAULT '-'
);

CREATE TABLE db_tevent (
   uuid varchar(36) PRIMARY KEY,
   ts int NOT NULL,
   tslocal varchar(100) NOT NULL,
   event varchar(50) NOT NULL,
   props json NOT NULL,
   uploaded boolean NOT NULL DEFAULT 0
);
