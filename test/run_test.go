package test

import (
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	v1 "github.com/flanksource/canary-checker/api/v1"
	"github.com/flanksource/canary-checker/cmd"
	"github.com/flanksource/canary-checker/pkg"
)

type args struct {
	config v1.CanarySpec
}

type test struct {
	name string
	args args
	want []pkg.CheckResult // each config can result in multiple checks
}

func TestRunChecks(t *testing.T) {
	httpPassConfig := pkg.ParseConfig("../fixtures/http_pass.yaml")
	httpFailConfig := pkg.ParseConfig("../fixtures/http_fail.yaml")
	postgresFailConfig := pkg.ParseConfig("../fixtures/postgres_fail.yaml")
	dnsFailConfig := pkg.ParseConfig("../fixtures/dns_fail.yaml")
	dnsPassConfig := pkg.ParseConfig("../fixtures/dns_pass.yaml")

	tests := []test{
		{
			name: "http_pass",
			args: args{httpPassConfig},
			want: []pkg.CheckResult{
				{
					Check:   httpPassConfig.HTTP[0],
					Pass:    true,
					Invalid: false,
					Metrics: []pkg.Metric{},
				},
			},
		},
		{
			name: "http_fail",
			args: args{httpFailConfig},
			want: []pkg.CheckResult{
				{
					Check:   httpFailConfig.HTTP[0],
					Pass:    false,
					Invalid: true,
					Metrics: []pkg.Metric{},
					Message: "Failed to resolve DNS",
				},
				{
					Check:   httpFailConfig.HTTP[1],
					Pass:    false,
					Invalid: false,
					Metrics: []pkg.Metric{},
				},
			},
		},
		{
			name: "postgres_fail",
			args: args{postgresFailConfig},
			want: []pkg.CheckResult{
				{
					Check:   postgresFailConfig.Postgres[0],
					Pass:    false,
					Invalid: false,
					Metrics: []pkg.Metric{},
				},
			},
		},
		{
			name: "dns_fail",
			args: args{dnsFailConfig},
			want: []pkg.CheckResult{
				{
					Check:   dnsFailConfig.DNS[0],
					Pass:    false,
					Invalid: false,
					Metrics: []pkg.Metric{},
					Message: "Check failed: A 1.2.3.4.nip.io on 8.8.8.8. Got [1.2.3.4], expected [8.8.8.8]",
				},
				{
					Check:   dnsFailConfig.DNS[1],
					Pass:    false,
					Invalid: false,
					Metrics: []pkg.Metric{},
					Message: "Check failed: PTR 8.8.8.8 on 8.8.8.8. Records count is less then minrecords",
				},
				{
					Check:   dnsFailConfig.DNS[2],
					Pass:    false,
					Invalid: false,
					Metrics: []pkg.Metric{},
					Message: "Check failed: CNAME dns.google on 8.8.8.8. Got [dns.google.], expected [wrong.google.]",
				},
				{
					Check:   dnsFailConfig.DNS[3],
					Pass:    false,
					Invalid: false,
					Metrics: []pkg.Metric{},
					Message: "Check failed: MX flanksource.com on 8.8.8.8. Got [alt1.aspmx.l.google.com. 5 alt2.aspmx.l.google.com. 5 aspmx.l.google.com. 1 aspmx2.googlemail.com. 10 aspmx3.googlemail.com. 10], expected [alt1.aspmx.l.google.com. 5 alt2.aspmx.l.google.com. 5 aspmx.l.google.com. 1]",
				},
				{
					Check:   dnsFailConfig.DNS[4],
					Pass:    false,
					Invalid: false,
					Metrics: []pkg.Metric{},
					Message: "Check failed: TXT flanksource.com on 8.8.8.8. Records count is less then minrecords",
				},
				{
					Check:   dnsFailConfig.DNS[5],
					Pass:    false,
					Invalid: false,
					Metrics: []pkg.Metric{},
					Message: "Check failed: NS flanksource.com on 8.8.8.8. Got [ns-1450.awsdns-53.org. ns-1896.awsdns-45.co.uk. ns-908.awsdns-49.net. ns-91.awsdns-11.com.], expected [ns-91.awsdns-11.com.]",
				},
			},
		},
		{
			name: "dns_pass",
			args: args{dnsPassConfig},
			want: []pkg.CheckResult{
				{
					Check:   dnsPassConfig.DNS[0],
					Pass:    true,
					Invalid: false,
					Metrics: []pkg.Metric{},
					Message: "Successful check on 8.8.8.8. Got [1.2.3.4]",
				},
				{
					Check:   dnsPassConfig.DNS[1],
					Pass:    true,
					Invalid: false,
					Metrics: []pkg.Metric{},
					Message: "Successful check on 8.8.8.8. Got [dns.google.]",
				},
				{
					Check:   dnsPassConfig.DNS[2],
					Pass:    true,
					Invalid: false,
					Metrics: []pkg.Metric{},
					Message: "Successful check on 8.8.8.8. Got [dns.google.]",
				},
				{
					Check:   dnsPassConfig.DNS[3],
					Pass:    true,
					Invalid: false,
					Metrics: []pkg.Metric{},
					Message: "Successful check on 8.8.8.8. Got [alt1.aspmx.l.google.com. 5 alt2.aspmx.l.google.com. 5 aspmx.l.google.com. 1 aspmx2.googlemail.com. 10 aspmx3.googlemail.com. 10]",
				},
				{
					Check:   dnsPassConfig.DNS[4],
					Pass:    true,
					Invalid: false,
					Metrics: []pkg.Metric{},
					Message: "Successful check on 8.8.8.8. Got [google-site-verification=IIE1aJuvqseLUKSXSIhu2O2lgdU_d8csfJjjIQVc-q0]",
				},
				{
					Check:   dnsPassConfig.DNS[5],
					Pass:    true,
					Invalid: false,
					Metrics: []pkg.Metric{},
					Message: "Successful check on 8.8.8.8. Got [ns-1450.awsdns-53.org. ns-1896.awsdns-45.co.uk. ns-908.awsdns-49.net. ns-91.awsdns-11.com.]",
				},
			},
		},
	}
	runTests(t, tests)
}

// Test the connectivity with a mock DB
func TestPostgresCheckWithDbMock(t *testing.T) {
	// create a mock db
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	// This is the result we expect
	rows := sqlmock.NewRows([]string{"column"}).
		AddRow(1)

	// declare our expectation
	mock.ExpectQuery("^SELECT 1$").WillReturnRows(rows)

	config := pkg.ParseConfig("../fixtures/postgres_succeed.yaml")

	results := cmd.RunChecks(config)

	expectationErr := mock.ExpectationsWereMet()
	if expectationErr != nil {
		t.Errorf("Test %s failed. Expected queries not made: %v", "postgres_succeed", expectationErr)
	}

	for _, result := range results {
		if result.Invalid {
			t.Errorf("Test %s failed. Expected valid result, but found %v", "postgres_succeed", result.Invalid)
		}
		if !result.Pass {
			t.Errorf("Test %s failed. Expected PASS result, but found %v", "postgres_succeed", result.Pass)
		}
	}
}

func runTests(t *testing.T, tests []test) {
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkResults := cmd.RunChecks(tt.args.config)
			i := 0

			for _, res := range checkResults {
				// check if this result is extra
				if i > len(tt.want)-1 {
					t.Errorf("Test %s failed. Found unexpected extra result is %v", tt.name, res)
				} else {
					/* Not checking durations we don't want equality*/
					if res.Invalid != tt.want[i].Invalid ||
						res.Pass != tt.want[i].Pass ||
						(tt.want[i].Message != "" && res.Message != tt.want[i].Message) {
						t.Errorf("Test %s failed. Expected result is %v, but found %v", tt.name, tt.want[i], res)
					}
				}
				i++
			}
			// check if we have more expected results than were found
			if len(tt.want) > len(checkResults) {
				t.Errorf("Test %s failed. Expected %d results, but found %d ", tt.name, len(tt.want), len(checkResults))
				for i := len(checkResults); i <= len(tt.want)-1; i++ {
					t.Errorf("Did not find %s %v", tt.name, tt.want[i])
				}
			}
		})
	}
}
