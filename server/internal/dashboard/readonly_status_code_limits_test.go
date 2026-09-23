package dashboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

func TestReadonlyStatusCodeLargeTextMySQL(t *testing.T) {
	db := readonlyEquivalenceDB(t)
	var stepLimit int
	require.NoError(t, db.QueryRow("SELECT @@regexp_time_limit").Scan(&stepLimit))
	if stepLimit > 0 && stepLimit < 32 {
		t.Skip("requires at least MySQL's default regexp_time_limit of 32")
	}
	_, err := db.Exec("ALTER TABLE logs MODIFY content LONGTEXT NULL, MODIFY other LONGTEXT NULL")
	require.NoError(t, err)
	for i, values := range [][2]any{
		{strings.Repeat(" ", 1_000_000), nil},
		{nil, strings.Repeat("status_code=429; ", 80_000)},
		{strings.Repeat(" ", 100_000) + "unrelated-500", nil},
		{"status_code=500 " + strings.Repeat(" ", 100_000), nil},
		{nil, strings.Repeat(" ", 100_000) + "HTTP 500"},
		{"HTTP ", "500"},
		{nil, nil},
	} {
		_, err := db.Exec("INSERT INTO logs (id,user_id,created_at,type,request_id,quota,content,other) VALUES (?,1,150,5,?,10,?,?)", i+1, fmt.Sprintf("limit-%d", i), values[0], values[1])
		require.NoError(t, err)
	}
	// Freeze the regressing optimized pattern: the same row works with the
	// original independent matches, but the combined pattern exceeds its budget.
	combined := `(^|[^[:alnum:]_])((status_code|statusCode|status[[:space:]]+code|error_code|["']code["'])[[:space:]]*[:=][[:space:]]*["']?|HTTP[[:space:]]+)500([^[:digit:]]|$)`
	if stepLimit == 32 {
		var match int
		err := db.QueryRow("SELECT content REGEXP ? FROM logs WHERE id=3", combined).Scan(&match)
		var mysqlErr *mysql.MySQLError
		require.ErrorAs(t, err, &mysqlErr)
		require.Equal(t, uint16(3699), mysqlErr.Number)
	}
	for _, collation := range []string{"utf8mb4_0900_ai_ci", "utf8mb4_unicode_ci", "utf8mb4_bin"} {
		t.Run(collation, func(t *testing.T) {
			_, err := db.Exec("ALTER TABLE logs CONVERT TO CHARACTER SET utf8mb4 COLLATE " + collation)
			require.NoError(t, err)
			h := &PassthroughHandler{Config: customerQueryConfig{}, pools: map[string]passthroughPool{"a": {encrypted: "test-pool", db: db}}}
			query := "/?site=a&start_time=100&end_time=200&status_code=500&limit=100&offset=0"
			list, count, stat := httptest.NewRecorder(), httptest.NewRecorder(), httptest.NewRecorder()
			h.Logs(list, httptest.NewRequest("GET", query, nil))
			h.LogCount(count, httptest.NewRequest("GET", query, nil))
			h.LogStat(stat, httptest.NewRequest("GET", query, nil))
			require.Equal(t, 200, list.Code, list.Body.String())
			require.Equal(t, 200, count.Code, count.Body.String())
			require.Equal(t, 200, stat.Code, stat.Body.String())
			var listed struct {
				Items []PassthroughLog `json:"items"`
			}
			require.NoError(t, json.Unmarshal(list.Body.Bytes(), &listed))
			require.Len(t, listed.Items, 2)
			require.Equal(t, []int64{5, 4}, []int64{listed.Items[0].ID, listed.Items[1].ID})
			require.JSONEq(t, `{"configured":true,"total":2}`, count.Body.String())
			require.JSONEq(t, `{"configured":true,"summary":{"quota":20,"rpm":0,"tpm":0}}`, stat.Body.String())
		})
	}
}

func TestReadonlyQueryFailureLogsCodeWithoutSourceData(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	logReadonlyQueryFailure("a", "logs", "query", &mysql.MySQLError{Number: 3699, SQLState: [5]byte{'H', 'Y', '0', '0', '0'}, Message: "private-source-data"})
	logReadonlyQueryFailure("a", "logs", "scan", errors.New("scan value containing private-source-data"))
	require.Contains(t, output.String(), "mysql_errno=3699")
	require.Contains(t, output.String(), `sqlstate="HY000"`)
	require.Contains(t, output.String(), "stage=scan")
	require.NotContains(t, output.String(), "private-source-data")
}
