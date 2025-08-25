package main

import (
	"log"

	pgadapter "github.com/casbin/casbin-pg-adapter"
	"github.com/casbin/casbin/v2"
	"github.com/go-pg/pg/v10"
	_ "github.com/lib/pq"
)

func main() {
	opts, _ := pg.ParseURL("postgresql://shopnexus:peakshopnexuspassword@localhost:5432/shopnexus?sslmode=disable")

	db := pg.Connect(opts)
	defer db.Close()

	a, err := pgadapter.NewAdapterByDB(db, pgadapter.WithTableName("casbin_rule"))
	if err != nil {
		panic(err)
	}
	e, err := casbin.NewEnforcer("rbac_model.conf", a)
	if err != nil {
		panic(err)
	}

	// Load the policy from DB
	err = e.LoadPolicy()
	if err != nil {
		panic(err)
	}

	// Check permissions
	sub := "alice" // the user that wants to access a resource
	obj := "data1" // the resource that is going to be accessed
	act := "read"  // the operation that the user performs on the resource
	ok, err := e.Enforce(sub, obj, act)
	if err != nil {
		panic(err)
	}

	if ok {
		println("Permission granted")
	} else {
		println("Permission denied")
	}

	e.AddPolicy("read", "write")

	// Save the policy back to DB.
	if err = e.SavePolicy(); err != nil {
		log.Println("SavePolicy failed, err: ", err)
	}

}
