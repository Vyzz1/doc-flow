package main

import (
	"fmt"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscognito"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// identity is the user pool and the app client whose tokens the API trusts.
type identity struct {
	pool   awscognito.UserPool
	client awscognito.UserPoolClient
}

// newIdentity creates the user directory that signs the API's tokens.
//
// Cognito is chosen over an external provider because it needs no account
// anywhere else and costs nothing at this size; see
// docs/adr/0002-cognito-for-authentication.md.
func newIdentity(scope constructs.Construct, stage Stage, removalPolicy awscdk.RemovalPolicy) identity {
	pool := awscognito.NewUserPool(scope, jsii.String("Users"), &awscognito.UserPoolProps{
		// Lite is set rather than left to default. AWS defaults a new pool
		// to Essentials, and the features that justifies, managed login and
		// passkeys, are ones this API does not use: it only needs a signed
		// token. Both plans are free below ten thousand monthly active
		// users, but the rate beyond differs by about three times.
		FeaturePlan:       awscognito.FeaturePlan_LITE,
		SelfSignUpEnabled: jsii.Bool(false),
		SignInAliases:     &awscognito.SignInAliases{Email: jsii.Bool(true)},
		StandardAttributes: &awscognito.StandardAttributes{
			Email: &awscognito.StandardAttribute{Required: jsii.Bool(true), Mutable: jsii.Bool(false)},
		},
		PasswordPolicy: &awscognito.PasswordPolicy{
			MinLength:        jsii.Number(12),
			RequireLowercase: jsii.Bool(true),
			RequireUppercase: jsii.Bool(true),
			RequireDigits:    jsii.Bool(true),
			RequireSymbols:   jsii.Bool(true),
		},
		AccountRecovery: awscognito.AccountRecovery_EMAIL_ONLY,
		RemovalPolicy:   removalPolicy,
	})

	client := pool.AddClient(jsii.String("Api"), &awscognito.UserPoolClientOptions{
		UserPoolClientName: jsii.String("docflow-api"),
		// No secret: the clients here are browsers and command lines, which
		// cannot keep one.
		GenerateSecret: jsii.Bool(false),
		AuthFlows: &awscognito.AuthFlow{
			// SRP is what a real client uses. Plain username and password
			// is enabled only in dev, where it is the difference between
			// getting a token with one CLI call and standing up a login
			// page first.
			UserSrp:      jsii.Bool(true),
			UserPassword: jsii.Bool(stage != StageProd),
		},
		AccessTokenValidity:        awscdk.Duration_Hours(jsii.Number(1)),
		IdTokenValidity:            awscdk.Duration_Hours(jsii.Number(1)),
		RefreshTokenValidity:       awscdk.Duration_Days(jsii.Number(30)),
		PreventUserExistenceErrors: jsii.Bool(true),
	})

	return identity{pool: pool, client: client}
}

// issuer is the URL API Gateway fetches the signing keys from.
func (i identity) issuer(stack awscdk.Stack) *string {
	return jsii.String(fmt.Sprintf("https://cognito-idp.%s.amazonaws.com/%s",
		*stack.Region(), *i.pool.UserPoolId()))
}
