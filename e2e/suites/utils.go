package suites

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

func Exec(cmd string) (string, error) {
	command := exec.Command("bash", "-c", cmd)
	bytes, err := command.CombinedOutput()
	out := string(bytes)

	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return out, fmt.Errorf("non-zero exit code (%d):\n %w", exitError.ExitCode(), exitError)
	}

	return out, err
}

func ExecPretty(cmd string) error {
	out, err := Exec(cmd)
	_, _ = fmt.Fprintf(os.Stdout, "[shell]: %s\n-------------- output:\n%s-------------- [end of shell]\n", cmd, out)
	return err
}

func ExecPrettyWithoutCmd(cmd string) error {
	out, err := Exec(cmd)
	_, _ = fmt.Fprintf(os.Stdout, "-------------- [shell output]\n%s-------------- [end of shell]\n", out)
	return err
}
