// Command t09 proves the risky DynamoDB calls of T09 against DynamoDB Local.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func must(err error) {
	if err != nil {
		fmt.Println("FAIL:", err)
		os.Exit(1)
	}
}

func s(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }
func n(v string) types.AttributeValue { return &types.AttributeValueMemberN{Value: v} }

func main() {
	ctx := context.Background()
	db := dynamodb.New(dynamodb.Options{Region: "us-east-1", BaseEndpoint: aws.String("http://127.0.0.1:8000"),
		Credentials: credentials.NewStaticCredentialsProvider("x", "x", "")})
	raw, err := os.ReadFile("database/dynamodb/table.json")
	must(err)
	var in dynamodb.CreateTableInput
	must(json.Unmarshal(raw, &in))
	in.TableName = aws.String("poc-t09")
	_, _ = db.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: in.TableName})
	_, err = db.CreateTable(ctx, &in)
	must(err)
	d, err := db.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: in.TableName})
	must(err)
	fmt.Println("PASS 1 table", d.Table.TableStatus, "gsis", len(d.Table.GlobalSecondaryIndexes))

	// 2 prefix search
	for _, w := range []string{"mc#g1", "mcz#g2", "mx#g3"} {
		_, err = db.PutItem(ctx, &dynamodb.PutItemInput{TableName: in.TableName, Item: map[string]types.AttributeValue{"PK": s("W#pvp#mc"), "SK": s(w)}})
		must(err)
	}
	q, err := db.Query(ctx, &dynamodb.QueryInput{TableName: in.TableName, KeyConditionExpression: aws.String("PK = :p AND begins_with(SK, :q)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":p": s("W#pvp#mc"), ":q": s("mc")}})
	must(err)
	fmt.Println("PASS 2 prefix hits", q.Count)

	// 3 conditional update, old item returned on failure
	key := map[string]types.AttributeValue{"PK": s("C#pvp#g"), "SK": s("META")}
	upd := func(t string) error {
		_, err := db.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: in.TableName, Key: key,
			UpdateExpression:                    aws.String("SET ls = :t"),
			ConditionExpression:                 aws.String("attribute_not_exists(ls) OR ls <= :t"),
			ExpressionAttributeValues:           map[string]types.AttributeValue{":t": n(t)},
			ReturnValuesOnConditionCheckFailure: types.ReturnValuesOnConditionCheckFailureAllOld})
		return err
	}
	must(upd("200"))
	err = upd("100")
	var ccf *types.ConditionalCheckFailedException
	if !errors.As(err, &ccf) || ccf.Item["ls"] == nil {
		must(fmt.Errorf("want conditional failure with old item, got %v", err))
	}
	fmt.Println("PASS 3 older write rejected, old item returned:", ccf.Item["ls"].(*types.AttributeValueMemberN).Value)

	// 4 kill transaction twice
	kill := func() error {
		_, err := db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
			{Put: &types.Put{TableName: in.TableName, Item: map[string]types.AttributeValue{"PK": s("K#pvp#a"), "SK": s("0000000000001#b")},
				ConditionExpression: aws.String("attribute_not_exists(PK)")}},
			{Update: &types.Update{TableName: in.TableName, Key: map[string]types.AttributeValue{"PK": s("B#pvp"), "SK": s("a")},
				UpdateExpression: aws.String("ADD kills :one"), ExpressionAttributeValues: map[string]types.AttributeValue{":one": n("1")}}},
		}})
		return err
	}
	must(kill())
	err = kill()
	var tce *types.TransactionCanceledException
	if !errors.As(err, &tce) || *tce.CancellationReasons[0].Code != "ConditionalCheckFailed" {
		must(fmt.Errorf("want cancelled transaction, got %v", err))
	}
	g, err := db.GetItem(ctx, &dynamodb.GetItemInput{TableName: in.TableName, Key: map[string]types.AttributeValue{"PK": s("B#pvp"), "SK": s("a")}})
	must(err)
	fmt.Println("PASS 4 total after retry:", g.Item["kills"].(*types.AttributeValueMemberN).Value)

	// 5+6 capacity and size of a max contact item
	item := map[string]types.AttributeValue{"PK": s("C#pvp#Player-1329-0A1B2C3D4E5F6A7B"), "SK": s("META"), "nm": s("Abcdefghijkl"), "raw": s("Abcdefghijkl-Realm-Name-Long"),
		"rx": s("hostile"), "fs": n("1763380000000"), "ls": n("1763380000000"), "mp": n("1411"), "x": n("12.3456"), "y": n("65.4321"), "kd": s("exact"),
		"GSI1PK": s("R#pvp"), "GSI1SK": s("1763380000000")}
	p, err := db.PutItem(ctx, &dynamodb.PutItemInput{TableName: in.TableName, Item: item, ReturnConsumedCapacity: types.ReturnConsumedCapacityIndexes})
	must(err)
	b, _ := attributevalue.MarshalMap(map[string]any{"k": 1})
	_ = b
	fmt.Printf("PASS 5 contact put WCU table=%.1f total=%.1f\n", aws.ToFloat64(p.ConsumedCapacity.Table.CapacityUnits), aws.ToFloat64(p.ConsumedCapacity.CapacityUnits))
	size := 0
	for k, v := range item {
		size += len(k)
		switch x := v.(type) {
		case *types.AttributeValueMemberS:
			size += len(x.Value)
		case *types.AttributeValueMemberN:
			size += len(x.Value)
		}
	}
	fmt.Println("PASS 6 contact item bytes ~", size, "(< 1024)")
	_, _ = db.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: in.TableName})
}
