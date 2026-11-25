//go:build windows

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"syscall"
)

var messageBeep = syscall.NewLazyDLL("user32.dll").NewProc("MessageBeep")

func playWarningSound() {
	const mbIconWarning = 0x30
	if ok, _, err := messageBeep.Call(mbIconWarning); ok == 0 {
		log.Printf("warning sound error: %v", err)
	}
}

func showWarning(warning screenTimeWarning) error {
	playWarningSound()
	command := warningCommand(context.Background(), warning, false)
	if err := command.Start(); err != nil {
		return err
	}
	go func() {
		if err := command.Wait(); err != nil {
			log.Printf("warning window error: %v", err)
		}
	}()
	return nil
}

// Accessed only by the client loop. A running window is reused on every poll.
type expiredWarningWindow struct {
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	announced bool
}

func (w *expiredWarningWindow) setVisible(visible bool) error {
	if w.done != nil {
		select {
		case <-w.done:
			w.cancel()
			w.cancel, w.done = nil, nil
		default:
		}
	}
	if !visible {
		w.announced = false
		if w.cancel != nil {
			w.cancel()
			w.cancel, w.done = nil, nil
		}
		return nil
	}
	if w.done != nil {
		return nil
	}
	if !w.announced {
		playWarningSound()
		w.announced = true
	}
	ctx, cancel := context.WithCancel(w.ctx)
	command := warningCommand(ctx, screenTimeWarning{
		message: "Tiden er ute. Slå av PC-en nå.",
		color:   "#D00000",
	}, true)
	if err := command.Start(); err != nil {
		cancel()
		return err
	}
	done := make(chan struct{})
	w.cancel, w.done = cancel, done
	go func() {
		if err := command.Wait(); err != nil && ctx.Err() == nil {
			log.Printf("expired warning window error: %v", err)
		}
		close(done)
	}()
	return nil
}

func warningCommand(ctx context.Context, warning screenTimeWarning, persistent bool) *exec.Cmd {
	width, height := 560, 170
	if persistent {
		// 700 * 204 = 1.5 * 560 * 170: 50 percent more area.
		width, height = 700, 204
	}
	script := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName PresentationFramework
$window = New-Object Windows.Window
$window.Width = %d
$window.Height = %d
$window.WindowStyle = 'None'
$window.ResizeMode = 'NoResize'
$window.ShowInTaskbar = $false
$window.Topmost = $true
$window.ShowActivated = $false
$window.AllowsTransparency = $true
$window.Opacity = 0.96
$window.Background = [Windows.Media.BrushConverter]::new().ConvertFromString('%s')
$window.Left = [System.Windows.SystemParameters]::WorkArea.Right - $window.Width - 20
$window.Top = [System.Windows.SystemParameters]::WorkArea.Top + 54
$text = New-Object Windows.Controls.TextBlock
$text.Text = '%s'
$text.Foreground = [Windows.Media.Brushes]::White
$text.FontSize = 27
$text.TextWrapping = 'Wrap'
$text.TextAlignment = 'Center'
$text.Margin = '18'
$text.FontWeight = 'SemiBold'
$text.VerticalAlignment = 'Center'
$text.HorizontalAlignment = 'Center'
$window.Content = $text`, width, height, warning.color, warning.message)
	if persistent {
		script += fmt.Sprintf(`
$window.add_Closing({ $_.Cancel = $true })
$clientProcess = [System.Diagnostics.Process]::GetProcessById(%d)
$parentTimer = New-Object Windows.Threading.DispatcherTimer
$parentTimer.Interval = [TimeSpan]::FromSeconds(1)
$parentTimer.add_Tick({
  if ($clientProcess.HasExited) { [Environment]::Exit(0) }
})
$window.add_Loaded({ $parentTimer.Start() })`, os.Getpid())
	} else {
		script += `
$timer = New-Object Windows.Threading.DispatcherTimer
$timer.Interval = [TimeSpan]::FromMilliseconds(4500)
$timer.add_Tick({
  $timer.Stop()
  $fade = New-Object Windows.Media.Animation.DoubleAnimation(0.96, 0, [TimeSpan]::FromMilliseconds(500))
  $fade.add_Completed({ $window.Close() })
  $window.BeginAnimation([Windows.Window]::OpacityProperty, $fade)
})
$window.add_Loaded({ $timer.Start() })`
	}
	script += "\n[void]$window.ShowDialog()"
	return warningPowerShell(ctx, script)
}

func warningPowerShell(ctx context.Context, script string) *exec.Cmd {
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-STA", "-WindowStyle", "Hidden", "-Command", script)
	command.Stderr = log.Writer()
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return command
}
