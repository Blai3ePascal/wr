// Copyright © 2016-2017 Genome Research Limited
// Author: Sendu Bala <sb10@sanger.ac.uk>.
//
//  This file is part of wr.
//
//  wr is free software: you can redistribute it and/or modify
//  it under the terms of the GNU Lesser General Public License as published by
//  the Free Software Foundation, either version 3 of the License, or
//  (at your option) any later version.
//
//  wr is distributed in the hope that it will be useful,
//  but WITHOUT ANY WARRANTY; without even the implied warranty of
//  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
//  GNU Lesser General Public License for more details.
//
//  You should have received a copy of the GNU Lesser General Public License
//  along with wr. If not, see <http://www.gnu.org/licenses/>.

package cmd

import (
	"bufio"
	"fmt"
	"github.com/VertebrateResequencing/wr/jobqueue"
	"github.com/spf13/cobra"
	"io"
	"os"
	"strings"
	"time"
)

// options for this cmd
var cmdFileStatus string
var cmdIDStatus string
var cmdLine string
var showBuried bool
var showStd bool
var showEnv bool
var quietMode bool
var statusLimit int

// statusCmd represents the status command
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Get status of commands",
	Long: `You can find the status of commands you've previously added using
"wr add" or "wr setup" by running this command.

Specify one of the flags -f, -l  or -i to choose which commands you want the
status of. If none are supplied, it gives you an overview of all your currently
incomplete commands.

The file to provide -f is in the same format as that taken by "wr add".

In -f and -l mode you must provide the cwd the commands were set to run in. You
can do this by using the -c option, or in -f mode your file can specify the cwd
in the JSON, in case it's different for each command. If not supplied at all,
cwd will default to your current directory, but you won't get any status if
you're not in the same directory you were in when you first added the commands,
or if you added them with a different cwd.

By default, commands with the same state, reason for failure and exitcode are
grouped together and only a random 1 of them is displayed (and you are told how
many were skipped). --limit changes how many commands in each of these groups
are displayed. A limit of 0 turns off grouping and shows all your desired
commands individually, but you could hit a timeout if retrieving the details of
very many (tens of thousands+) commands.`,
	Run: func(cmd *cobra.Command, args []string) {
		set := 0
		if cmdFileStatus != "" {
			set++
		}
		if cmdIDStatus != "" {
			set++
		}
		if cmdLine != "" {
			set++
		}
		if set > 1 {
			die("-f, -i and -l are mutually exclusive; only specify one of them")
		}
		if cmdCwd == "" {
			pwd, err := os.Getwd()
			if err != nil {
				die("%s", err)
			}
			cmdCwd = pwd
		}
		cmdState := ""
		if showBuried {
			cmdState = "buried"
		}
		timeout := time.Duration(timeoutint) * time.Second

		jq, err := jobqueue.Connect(addr, "cmds", timeout)
		if err != nil {
			die("%s", err)
		}
		defer jq.Disconnect()

		var jobs []*jobqueue.Job
		showextra := true
		switch {
		case set == 0:
			// get incomplete jobs
			jobs, err = jq.GetIncomplete(statusLimit, cmdState, showStd, showEnv)
		case cmdIDStatus != "":
			// get all jobs with this identifier (repgroup)
			jobs, err = jq.GetByRepGroup(cmdIDStatus, statusLimit, cmdState, showStd, showEnv)
		case cmdFileStatus != "":
			// get jobs that have the supplied commands. We support the same
			// format of file that "wr add" takes, but only care about the
			// first 2 columns
			var reader io.Reader
			if cmdFileStatus == "-" {
				reader = os.Stdin
			} else {
				reader, err = os.Open(cmdFileStatus)
				if err != nil {
					die("could not open file '%s': %s", cmdFileStatus, err)
				}
				defer reader.(*os.File).Close()
			}
			scanner := bufio.NewScanner(reader)
			var ccs [][2]string
			desired := 0
			for scanner.Scan() {
				cols := strings.Split(scanner.Text(), "\t")
				colsn := len(cols)
				if colsn < 1 || cols[0] == "" {
					continue
				}
				var cwd string
				if colsn < 2 || cols[1] == "" {
					cwd = cmdCwd
				} else {
					cwd = cols[1]
				}
				ccs = append(ccs, [2]string{cols[0], cwd})
				desired++
			}
			jobs, err = jq.GetByCmds(ccs)
			if len(jobs) < desired {
				warn("%d/%d cmds were not found", desired-len(jobs), desired)
			}
			showextra = false
		default:
			// get job that has the supplied command
			var job *jobqueue.Job
			job, err = jq.GetByCmd(cmdLine, cmdCwd, showStd, showEnv)
			jobs = append(jobs, job)
		}

		if err != nil {
			die("failed to get jobs corresponding to your settings: %s", err)
		}

		if quietMode {
			var d, re, b, ru, c int
			for _, job := range jobs {
				switch job.State {
				case "delayed":
					d += 1 + job.Similar
				case "ready":
					re += 1 + job.Similar
				case "buried":
					b += 1 + job.Similar
				case "reserved", "running":
					ru += 1 + job.Similar
				case "complete":
					c += 1 + job.Similar
				}
			}
			fmt.Printf("complete: %d\nrunning: %d\nready: %d\ndelayed: %d\nburied: %d\n", c, ru, re, d, b)
		} else {
			// print out status information for each job
			for _, job := range jobs {
				fmt.Printf("\n# %s\nCwd: %s\nId: %s; Requirements group: %s; Priority: %d; Attempts: %d\nExpected requirements: { memory: %dMB; time: %s; cpus: %d disk: %dGB }\n", job.Cmd, job.Cwd, job.RepGroup, job.ReqGroup, job.Priority, job.Attempts, job.Requirements.RAM, job.Requirements.Time, job.Requirements.Cores, job.Requirements.Disk)

				switch job.State {
				case "delayed":
					fmt.Printf("Status: %s following a temporary problem, will become ready soon\n", job.State)
				case "ready":
					fmt.Printf("Status: %s to be picked up by a `wr runner`\n", job.State)
				case "buried":
					fmt.Printf("Status: %s - you need to fix the problem and then `wr kick`\n", job.State)
				case "reserved", "running":
					fmt.Println("Status: running")
				case "complete":
					fmt.Printf("Status: %s\n", job.State)
				}

				if job.FailReason != "" {
					fmt.Printf("Previous problem: %s\n", job.FailReason)
				}

				if job.Exited {
					prefix := "Stats"
					if job.State != "complete" {
						prefix = "Stats of previous attempt"
					}
					fmt.Printf("%s: { Exit code: %d; Peak memory: %dMB; Wall time: %s; CPU time: %s }\nHost: %s; Pid: %d\n", prefix, job.Exitcode, job.PeakRAM, job.Walltime, job.CPUtime, job.Host, job.Pid)
					if showextra && showStd && job.Exitcode != 0 {
						stdout, err := job.StdOut()
						if err != nil {
							warn("problem reading the cmd's STDOUT: %s", err)
						} else if stdout != "" {
							fmt.Printf("StdOut:\n%s\n", stdout)
						} else {
							fmt.Printf("StdOut: [none]\n")
						}
						stderr, err := job.StdErr()
						if err != nil {
							warn("problem reading the cmd's STDERR: %s", err)
						} else if stderr != "" {
							fmt.Printf("StdErr:\n%s\n", stderr)
						} else {
							fmt.Printf("StdErr: [none]\n")
						}
					}
				} else if job.State == "running" {
					fmt.Printf("Stats: { Wall time: %s }\nHost: %s; Pid: %d\n", job.Walltime, job.Host, job.Pid)
					//*** we should be able to peek at STDOUT & STDERR, and see
					// Peak memory during a run... but is that possible/ too
					// expensive? Maybe we could communicate directly with the
					// runner?...
				}

				if showextra && showEnv {
					env, err := job.Env()
					if err != nil {
						warn("problem reading the cmd's Env: %s", err)
					} else {
						fmt.Printf("Env: %s\n", env)
					}
				}

				if job.Similar > 0 {
					fr := ""
					if job.FailReason != "" {
						fr = " and problem"
					}
					er := ""
					if job.Exited && job.Exitcode != 0 {
						if fr != "" {
							er = ", exit code"
						} else {
							er = " and exit code"
						}
					}
					fmt.Printf("+ %d other commands with the same status%s%s\n", job.Similar, er, fr)
				}
			}
		}

		fmt.Printf("\n")
	},
}

func init() {
	RootCmd.AddCommand(statusCmd)

	// flags specific to this sub-command
	statusCmd.Flags().StringVarP(&cmdFileStatus, "file", "f", "", "file containing commands you want the status of; - means read from STDIN")
	statusCmd.Flags().StringVarP(&cmdIDStatus, "identifier", "i", "", "identifier of the commands you want the status of")
	statusCmd.Flags().StringVarP(&cmdLine, "cmdline", "l", "", "a command line you want the status of")
	statusCmd.Flags().StringVarP(&cmdCwd, "cwd", "c", "", "working dir that the command(s) specified by -l or -f were set to run in")
	statusCmd.Flags().BoolVarP(&showBuried, "buried", "b", false, "in default or -i mode only, only show the status of buried commands")
	statusCmd.Flags().BoolVarP(&showStd, "std", "s", false, "except in -f mode, also show the most recent STDOUT and STDERR of incomplete commands")
	statusCmd.Flags().BoolVarP(&showEnv, "env", "e", false, "except in -f mode, also show the environment variables the command(s) ran with")
	statusCmd.Flags().BoolVarP(&quietMode, "quiet", "q", false, "minimal verbosity: just display status counts")
	statusCmd.Flags().IntVar(&statusLimit, "limit", 1, "number of commands that share the same properties to display; 0 displays all")

	statusCmd.Flags().IntVar(&timeoutint, "timeout", 30, "how long (seconds) to wait to get a reply from 'wr manager'")
}
