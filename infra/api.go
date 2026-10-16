package main

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsapigatewayv2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsapigatewayv2authorizers"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsapigatewayv2integrations"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/jsii-runtime-go"
)

// newHttpApi puts the two handlers behind an HTTP API whose every route
// requires a token this stack's user pool signed.
//
// An HTTP API rather than a REST API: it is about a third of the price, and
// nothing here needs the features the REST flavour charges for. The JWT
// authorizer is part of the gateway, so an unsigned request never reaches a
// function and never costs an invocation.
func newHttpApi(stack awscdk.Stack, users identity, createJob, getJob awslambda.Function) awsapigatewayv2.HttpApi {
	authorizer := awsapigatewayv2authorizers.NewHttpJwtAuthorizer(
		jsii.String("JwtAuthorizer"),
		users.issuer(stack),
		&awsapigatewayv2authorizers.HttpJwtAuthorizerProps{
			AuthorizerName: jsii.String("cognito"),
			// The audience is this one app client, so a token minted for
			// some other client of the same pool is refused.
			JwtAudience: &[]*string{users.client.UserPoolClientId()},
		},
	)

	api := awsapigatewayv2.NewHttpApi(stack, jsii.String("Api"), &awsapigatewayv2.HttpApiProps{
		ApiName: jsii.String("docflow"),
		// No default authorizer is set, because a route added without one
		// would then be open by accident. Each route names it.
		CorsPreflight: &awsapigatewayv2.CorsPreflightOptions{
			AllowMethods: &[]awsapigatewayv2.CorsHttpMethod{
				awsapigatewayv2.CorsHttpMethod_GET,
				awsapigatewayv2.CorsHttpMethod_POST,
			},
			AllowHeaders: jsii.Strings("authorization", "content-type"),
			AllowOrigins: jsii.Strings("*"),
			MaxAge:       awscdk.Duration_Hours(jsii.Number(1)),
		},
	})

	api.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:    jsii.String("/jobs"),
		Methods: &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_POST},
		Integration: awsapigatewayv2integrations.NewHttpLambdaIntegration(
			jsii.String("CreateJobIntegration"), createJob, nil),
		Authorizer: authorizer,
	})
	api.AddRoutes(&awsapigatewayv2.AddRoutesOptions{
		Path:    jsii.String("/jobs/{id}"),
		Methods: &[]awsapigatewayv2.HttpMethod{awsapigatewayv2.HttpMethod_GET},
		Integration: awsapigatewayv2integrations.NewHttpLambdaIntegration(
			jsii.String("GetJobIntegration"), getJob, nil),
		Authorizer: authorizer,
	})

	return api
}
