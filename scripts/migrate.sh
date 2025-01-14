#!/bin/bash
export PGPASSWORD=pricey
psql -h localhost -U pricey -d pricey -f migrations/001_initial_schema.sql
unset PGPASSWORD 