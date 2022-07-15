package rsync

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/backube/pvc-transfer/transfer"
	errorsutil "k8s.io/apimachinery/pkg/util/errors"
)

const (
	optRecursive     = "--recursive"
	optSymLinks      = "--links"
	optPermissions   = "--perms"
	optModTimes      = "--times"
	optDeviceFiles   = "--devices"
	optSpecialFiles  = "--specials"
	optOwner         = "--owner"
	optGroup         = "--group"
	optHardLinks     = "--hard-links"
	optPartial       = "--partial"
	optDelete        = "--delete"
	optBwLimit       = "--bwlimit=%d"
	optInfo          = "--info=%s"
	optHumanReadable = "--human-readable"
	optLogFile       = "--log-file=%s"
)

const (
	logFileStdOut = "/dev/stdout"
)

type Applier interface {
	ApplyTo(options *CommandOptions) error
}

// CommandOptions defines options that can be customized in the Rsync command
type CommandOptions struct {
	Recursive     bool
	SymLinks      bool
	Permissions   bool
	ModTimes      bool
	DeviceFiles   bool
	SpecialFiles  bool
	Groups        bool
	Owners        bool
	HardLinks     bool
	Delete        bool
	Partial       bool
	BwLimit       *int
	HumanReadable bool
	LogFile       string
	Info          []string
	Extras        []string
}

// Options returns validated rsync options and validation errors as two lists
func (c *CommandOptions) Options() ([]string, error) {
	var errs []error
	opts := []string{}
	if c.Recursive {
		opts = append(opts, optRecursive)
	}
	if c.SymLinks {
		opts = append(opts, optSymLinks)
	}
	if c.Permissions {
		opts = append(opts, optPermissions)
	}
	if c.DeviceFiles {
		opts = append(opts, optDeviceFiles)
	}
	if c.SpecialFiles {
		opts = append(opts, optSpecialFiles)
	}
	if c.ModTimes {
		opts = append(opts, optModTimes)
	}
	if c.Owners {
		opts = append(opts, optOwner)
	}
	if c.Groups {
		opts = append(opts, optGroup)
	}
	if c.HardLinks {
		opts = append(opts, optHardLinks)
	}
	if c.Delete {
		opts = append(opts, optDelete)
	}
	if c.Partial {
		opts = append(opts, optPartial)
	}
	if c.BwLimit != nil {
		if *c.BwLimit > 0 {
			opts = append(opts,
				fmt.Sprintf(optBwLimit, *c.BwLimit))
		} else {
			errs = append(errs, fmt.Errorf("rsync bwlimit value must be a positive integer"))
		}
	}
	if c.HumanReadable {
		opts = append(opts, optHumanReadable)
	}
	if c.LogFile != "" {
		opts = append(opts, fmt.Sprintf(optLogFile, c.LogFile))
	}
	if len(c.Info) > 0 {
		validatedOptions, err := filterRsyncInfoOptions(c.Info)
		errs = append(errs, err)
		opts = append(opts,
			fmt.Sprintf(
				optInfo, strings.Join(validatedOptions, ",")))
	}
	if len(c.Extras) > 0 {
		extraOpts, err := filterRsyncExtraOptions(c.Extras)
		errs = append(errs, err)
		opts = append(opts, extraOpts...)
	}
	return opts, errorsutil.NewAggregate(errs)
}

func NewDefaultOptionsFrom(opts ...Applier) transfer.CommandOptions {
	c := &CommandOptions{}
	c.Apply(rsyncCommandDefaultOptions()...)
	c.Apply(opts...)
	return c
}

func filterRsyncInfoOptions(options []string) (validatedOptions []string, err error) {
	var errs []error
	r := regexp.MustCompile(`^[A-Z]+\d?$`)
	for _, opt := range options {
		if r.MatchString(opt) {
			validatedOptions = append(validatedOptions, strings.TrimSpace(opt))
		} else {
			errs = append(errs, fmt.Errorf("invalid value %s for Rsync option --info", opt))
		}
	}
	return validatedOptions, errorsutil.NewAggregate(errs)
}

func filterRsyncExtraOptions(options []string) (validatedOptions []string, err error) {
	var errs []error
	r := regexp.MustCompile(`^\-{1,2}([a-z0-9]+\-){0,}?[a-z0-9]+$`)
	for _, opt := range options {
		if r.MatchString(opt) {
			validatedOptions = append(validatedOptions, opt)
		} else {
			errs = append(errs, fmt.Errorf("invalid Rsync option %s", opt))
		}
	}
	return validatedOptions, errorsutil.NewAggregate(errs)
}

func rsyncCommandDefaultOptions() []Applier {
	return []Applier{
		ArchiveFiles(true),
		StandardProgress(true),
	}
}

func (c *CommandOptions) Apply(opts ...Applier) error {
	errs := []error{}
	for _, opt := range opts {
		if err := opt.ApplyTo(c); err != nil {
			errs = append(errs, err)
		}
	}
	return errorsutil.NewAggregate(errs)
}

func rsyncDefaultOptions() ([]string, error) {
	c := CommandOptions{}
	defaultOptions := rsyncCommandDefaultOptions()
	err := c.Apply(defaultOptions...)
	if err != nil {
		return nil, err
	}
	return c.Options()
}

type ArchiveFiles bool

func (a ArchiveFiles) ApplyTo(opts *CommandOptions) error {
	opts.Recursive = bool(a)
	opts.SymLinks = bool(a)
	opts.Permissions = bool(a)
	opts.ModTimes = bool(a)
	opts.Groups = bool(a)
	opts.Owners = bool(a)
	opts.DeviceFiles = bool(a)
	opts.SpecialFiles = bool(a)
	return nil
}

type PreserveOwnership bool

func (p PreserveOwnership) ApplyTo(opts *CommandOptions) error {
	opts.Owners = bool(p)
	opts.Groups = bool(p)
	return nil
}

type StandardProgress bool

func (s StandardProgress) ApplyTo(opts *CommandOptions) error {
	opts.Info = []string{
		"COPY2", "DEL2", "REMOVE2", "SKIP2", "FLIST2", "PROGRESS2", "STATS2",
	}
	opts.HumanReadable = true
	return nil
}

type DeleteDestination bool

func (d DeleteDestination) ApplyTo(opts *CommandOptions) error {
	opts.Delete = bool(d)
	return nil
}
