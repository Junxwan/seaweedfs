package postgres

import (
	"database/sql"
	"fmt"

	"github.com/chrislusf/seaweedfs/weed/filer2"
	"github.com/chrislusf/seaweedfs/weed/filer2/abstract_sql"
	"github.com/chrislusf/seaweedfs/weed/util"
	_ "github.com/lib/pq"
)

const (
	CONNECTION_URL_PATTERN = "host=%s port=%d user=%s password=%s dbname=%s sslmode=%s connect_timeout=30"
)

func init() {
	filer2.Stores = append(filer2.Stores, &PostgresStore{})
}

type PostgresStore struct {
	abstract_sql.AbstractSqlStore
}

func (store *PostgresStore) GetName() string {
	return "postgres"
}

func (store *PostgresStore) Initialize(configuration util.Configuration) (err error) {
	return store.initialize(
		configuration.GetString("username"),
		configuration.GetString("password"),
		configuration.GetString("hostname"),
		configuration.GetInt("port"),
		configuration.GetString("database"),
		configuration.GetString("sslmode"),
		configuration.GetInt("connection_max_idle"),
		configuration.GetInt("connection_max_open"),
	)
}

func (store *PostgresStore) initialize(user, password, hostname string, port int, database, sslmode string, maxIdle, maxOpen int) (err error) {

	store.SqlInsert = "INSERT INTO filemeta (dirhash,name,directory,meta) VALUES($1,$2,$3,$4)"
	store.SqlUpdate = "UPDATE filemeta SET meta=$1 WHERE dirhash=$2 AND name=$3 AND directory=$4"
	store.SqlFind = "SELECT meta FROM filemeta WHERE dirhash=$1 AND name=$2 AND directory=$3"
	store.SqlDelete = "DELETE FROM filemeta WHERE dirhash=$1 AND name=$2 AND directory=$3"
	store.SqlListExclusive = "SELECT NAME, meta FROM filemeta WHERE dirhash=$1 AND name>$2 AND directory=$3 ORDER BY NAME ASC LIMIT $4"
	store.SqlListInclusive = "SELECT NAME, meta FROM filemeta WHERE dirhash=$1 AND name>=$2 AND directory=$3 ORDER BY NAME ASC LIMIT $4"

	sqlUrl := fmt.Sprintf(CONNECTION_URL_PATTERN, hostname, port, user, password, database, sslmode)
	var dbErr error
	store.DB, dbErr = sql.Open("postgres", sqlUrl)
	if dbErr != nil {
		store.DB.Close()
		store.DB = nil
		return fmt.Errorf("can not connect to %s error:%v", sqlUrl, err)
	}

	store.DB.SetMaxIdleConns(maxIdle)
	store.DB.SetMaxOpenConns(maxOpen)

	if err = store.DB.Ping(); err != nil {
		return fmt.Errorf("connect to %s error:%v", sqlUrl, err)
	}

	return nil
}
