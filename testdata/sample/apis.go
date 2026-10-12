package sample

import("context";"database/sql")

func execute(ctx context.Context, db *sql.DB, query string) { _ = ctx; _ = db; _ = query }
func APIWrite(ctx context.Context,db *sql.DB) { execute(ctx,db,"INSERT INTO api_records (body) VALUES (?)") }
func APIRead(db *sql.DB) { db.Query("SELECT body FROM api_records") }
func sendAPI(ctx context.Context, topic string) { _ = ctx; _ = topic }
func subscribeAPI(topic string, handler func(Event)) { _ = topic; _ = handler }
func APIHandler(e Event) { _ = e }
func APISetup() { subscribeAPI("order.ready",APIHandler) }
func APIPublish(ctx context.Context) { sendAPI(ctx,identity("order.ready")) }
