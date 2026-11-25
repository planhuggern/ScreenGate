//go:build windows

package main

import (
	"context"
	"fmt"
	"log"
)

// The client loop owns the latest text. A worker writes it to the same window
// for the lifetime of the client without waiting on PowerShell in the poll loop.
type countdownWindow struct {
	ctx     context.Context
	updates chan string
	done    chan struct{}
	text    string
}

func (w *countdownWindow) update(text string) error {
	if w.done != nil {
		select {
		case <-w.done:
			w.done = nil
		default:
		}
	}
	if w.done == nil {
		if err := w.start(); err != nil {
			return err
		}
	} else if w.text == text {
		return nil
	}
	w.text = text
	select {
	case <-w.updates:
	default:
	}
	w.updates <- text
	return nil
}

func (w *countdownWindow) start() error {
	command := warningPowerShell(w.ctx, countdownScript)
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	updates, done := make(chan string, 1), make(chan struct{})
	w.updates, w.done = updates, done
	go func() {
		if err := command.Wait(); err != nil && w.ctx.Err() == nil {
			log.Printf("countdown window error: %v", err)
		}
		close(done)
	}()
	go func() {
		defer stdin.Close()
		for {
			select {
			case <-done:
				return
			case <-w.ctx.Done():
				return
			case text := <-updates:
				if _, err := fmt.Fprintln(stdin, text); err != nil {
					return
				}
			}
		}
	}()
	return nil
}

const countdownScript = `$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName PresentationFramework
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;

public static class CountdownWindowStyle {
    private const int GWL_EXSTYLE = -20;
    private const int WS_EX_TRANSPARENT = 0x20;

    [DllImport("user32.dll")]
    private static extern int GetWindowLong(IntPtr window, int index);

    [DllImport("user32.dll")]
    private static extern int SetWindowLong(IntPtr window, int index, int style);

    public static void PassMouseThrough(IntPtr window) {
        SetWindowLong(window, GWL_EXSTYLE, GetWindowLong(window, GWL_EXSTYLE) | WS_EX_TRANSPARENT);
    }
}
'@
$window = New-Object Windows.Window
$window.Width = 150
$window.Height = 34
$window.WindowStyle = 'None'
$window.ResizeMode = 'NoResize'
$window.ShowInTaskbar = $false
$window.Topmost = $true
$window.ShowActivated = $false
$window.AllowsTransparency = $true
$window.Background = [Windows.Media.Brushes]::Transparent
$window.Left = [System.Windows.SystemParameters]::WorkArea.Right - $window.Width - 20
$window.Top = [System.Windows.SystemParameters]::WorkArea.Top + 10
$window.add_SourceInitialized({
    $handle = [Windows.Interop.WindowInteropHelper]::new($window).Handle
    [CountdownWindowStyle]::PassMouseThrough($handle)
})

$outline = New-Object Windows.Shapes.Path
$outline.Fill = [Windows.Media.Brushes]::White
$outline.Stroke = [Windows.Media.Brushes]::Black
$outline.StrokeThickness = 2
$outline.StrokeLineJoin = 'Round'
$outline.HorizontalAlignment = 'Right'
$outline.VerticalAlignment = 'Center'
$outline.Margin = '2'
$window.Content = $outline

$font = [Windows.Media.Typeface]::new('Segoe UI Semibold')
$culture = [System.Globalization.CultureInfo]::InvariantCulture
function Set-CountdownText([string]$label) {
    $dpi = [Windows.Media.VisualTreeHelper]::GetDpi($window).PixelsPerDip
    $text = [Windows.Media.FormattedText]::new($label, $culture, 'LeftToRight', $font, 20, $outline.Fill, $dpi)
    $outline.Data = $text.BuildGeometry([Windows.Point]::new(0, 0))
}

$reader = [System.IO.StreamReader]::new([Console]::OpenStandardInput())
$timer = New-Object Windows.Threading.DispatcherTimer
$timer.Interval = [TimeSpan]::FromMilliseconds(100)
$timer.add_Tick({
    if ($script:pendingLine.IsCompleted) {
        $label = $script:pendingLine.GetAwaiter().GetResult()
        if ($null -eq $label) {
            $window.Close()
        } else {
            Set-CountdownText $label
            $script:pendingLine = $reader.ReadLineAsync()
        }
    }
})
$window.add_Loaded({
    Set-CountdownText '--:--'
    $script:pendingLine = $reader.ReadLineAsync()
    $timer.Start()
})
$window.add_Closed({
    $timer.Stop()
    $window.Dispatcher.InvokeShutdown()
})
$window.Show()
[Windows.Threading.Dispatcher]::Run()`
