package containerlab

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	clabernetesconstants "github.com/clabernetes/clabernetes/constants"
	claberneteserrors "github.com/clabernetes/clabernetes/errors"
	clabernetesutil "github.com/clabernetes/clabernetes/util"
)

const expectedNonFullPortElementCount = 2

var (
	portPattern     *regexp.Regexp //nolint:gochecknoglobals
	portPatternOnce sync.Once      //nolint:gochecknoglobals
)

// TypedPort holds typed data about a containerlab port entry.
type TypedPort struct {
	Protocol        string
	ExposePort      int64
	DestinationPort int64
}

// AsContainerlabPortDefinition returns the `TypedPort` object as a valid containerlab port entry.
func (t *TypedPort) AsContainerlabPortDefinition() string {
	return fmt.Sprintf("%d:%d/%s", t.ExposePort, t.DestinationPort, strings.ToLower(t.Protocol))
}

// GetPortPattern returns a compiled regex to parse containerlab port definitions.
func GetPortPattern() *regexp.Regexp {
	portPatternOnce.Do(func() {
		portPattern = regexp.MustCompile(
			`(?P<exposePort>\d+):(?P<destinationPort>\d+)/?(?P<protocol>(TCP)|(UDP))?`,
		)
	})

	return portPattern
}

func processPortDefinitionFull(re *regexp.Regexp, portDefinition string) (*TypedPort, error) {
	paramsMap := clabernetesutil.RegexStringSubMatchToMap(re, portDefinition)

	protocol := clabernetesconstants.TCP
	if paramsMap["protocol"] == clabernetesconstants.UDP {
		protocol = clabernetesconstants.UDP
	}

	var retErr error

	exposePortAsInt, err := strconv.ParseInt(paramsMap["exposePort"], 10, 32)
	if err != nil || exposePortAsInt == 0 {
		retErr = fmt.Errorf(
			"%w: failed converting exposed port to integer, full port string '%s', parsed port "+
				"'%s'",
			claberneteserrors.ErrParse,
			portDefinition,
			paramsMap["exposePort"],
		)
	}

	destinationPortAsInt, err := strconv.ParseInt(paramsMap["destinationPort"], 10, 32)
	if err != nil || destinationPortAsInt == 0 {
		retErr = fmt.Errorf(
			"%w: failed converting destination port to integer, full port string '%s', parsed "+
				"port '%s'",
			claberneteserrors.ErrParse,
			portDefinition,
			paramsMap["destinationPort"],
		)
	}

	return &TypedPort{
		Protocol:        protocol,
		ExposePort:      exposePortAsInt,
		DestinationPort: destinationPortAsInt,
	}, retErr
}

// ProcessPortDefinition accepts a "portDefinition" from a containerlab topology and returns a
// `TypedPort` object. It returns an error if it cannot cast a port value to an integer.
func ProcessPortDefinition(portDefinition string) (*TypedPort, error) {
	re := GetPortPattern()

	portDefinition = strings.ToUpper(portDefinition)

	if re.MatchString(portDefinition) {
		// "fully" defined pattern -- meaning "port:port" with optional protocol
		return processPortDefinitionFull(re, portDefinition)
	}

	// not "full", so it could just be a port, or it could be a port/protocol

	portDefinitionSplit := strings.Split(portDefinition, "/")

	var retErr error

	protocol := clabernetesconstants.TCP

	if len(portDefinitionSplit) == expectedNonFullPortElementCount {
		userProtocol := strings.ToUpper(portDefinitionSplit[1])

		if userProtocol == clabernetesconstants.UDP {
			protocol = clabernetesconstants.UDP
		}
	}

	destinationPortAsInt, err := strconv.ParseInt(portDefinitionSplit[0], 10, 32)
	if err != nil || destinationPortAsInt == 0 {
		retErr = fmt.Errorf(
			"%w: failed converting destination port to integer, full port string '%s', parsed "+
				"port '%s'",
			claberneteserrors.ErrParse,
			portDefinition,
			portDefinitionSplit[0],
		)
	}

	return &TypedPort{
		Protocol:        protocol,
		DestinationPort: destinationPortAsInt,
	}, retErr
}
