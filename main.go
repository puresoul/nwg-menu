package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/allan-simon/go-singleinstance"
	"github.com/dlasky/gotk3-layershell/layershell"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

const version = "0.0.1"

var (
	appDirs                   []string
	configDirectory           string
	pinnedFile                string
	pinned                    []string
	leftBox                   *gtk.Box
	rightBox                  *gtk.Box
	src                       glib.SourceHandle
	refresh                   bool // we will use this to trigger rebuilding mainBox
	imgSizeScaled             int
	currentWsNum, targetWsNum int64
	win                       *gtk.Window
	id2entry                  map[string]desktopEntry
)

var categoryNames = [...]string{
	"utility",
	"development",
	"game",
	"graphics",
	"internet-and-network",
	"office",
	"audio-video",
	"system-tools",
	"other",
}

type category struct {
	Name        string
	DisplayName string
	Icon        string
}

var categories []category

type desktopEntry struct {
	DesktopID  string
	Name       string
	NameLoc    string
	Comment    string
	CommentLoc string
	Icon       string
	Exec       string
	Terminal   bool
	NoDisplay  bool
}

// slices below will hold DesktopID strings
var (
	listUtility            []string
	listDevelopment        []string
	listGame               []string
	listGraphics           []string
	listInternetAndNetwork []string
	listOffice             []string
	listAudioVideo         []string
	listSystemTools        []string
	listOther              []string
)

var desktopEntries []desktopEntry

// UI elements
var (
	categoriesListBox       *gtk.ListBox
	userDirsListBox         *gtk.ListBox
	resultWrapper           *gtk.Box
	resultWindow            *gtk.ScrolledWindow
	fileSearchResults       map[string]string
	fileSearchResultWindow  *gtk.ScrolledWindow
	backButton              *gtk.Box
	searchEntry             *gtk.SearchEntry
	phrase                  string
	resultListBox           *gtk.ListBox
	fileSearchResultListBox *gtk.ListBox
	userDirsMap             map[string]string
)

// Flags
var cssFileName = flag.String("s", "menu-start.css", "Styling: css file name")
var targetOutput = flag.String("o", "", "name of the Output to display the menu on")
var displayVersion = flag.Bool("v", false, "display Version information")
var autohide = flag.Bool("d", false, "auto-hiDe: close window when left")
var valign = flag.String("va", "bottom", "Vertical Alignment: \"bottom\" or \"top\"")
var halign = flag.String("ha", "left", "Horizontal Alignment: \"left\" or \"right\"")
var marginTop = flag.Int("mt", 0, "Margin Top")
var marginLeft = flag.Int("ml", 0, "Margin Left")
var marginRight = flag.Int("mr", 0, "Margin Right")
var marginBottom = flag.Int("mb", 0, "Margin Bottom")
var iconSizeLarge = flag.Int("isl", 32, "Icon Size Large")
var iconSizeSmall = flag.Int("iss", 16, "Icon Size Small")
var itemPadding = flag.Uint("padding", 2, "vertical item padding")
var lang = flag.String("lang", "", "force lang, e.g. \"en\", \"pl\"")
var fileManager = flag.String("fm", "thunar", "File Manager")
var terminal = flag.String("term", "alacritty", "Terminal emulator")

func main() {
	timeStart := time.Now()
	flag.Parse()

	if *displayVersion {
		fmt.Printf("nwg-panel-plugin-menu version %s\n", version)
		os.Exit(0)
	}

	// Gentle SIGTERM handler thanks to reiki4040 https://gist.github.com/reiki4040/be3705f307d3cd136e85
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM)
	go func() {
		for {
			s := <-signalChan
			if s == syscall.SIGTERM {
				println("SIGTERM received, bye bye!")
				gtk.MainQuit()
			}
		}
	}()

	// We want the same key/mouse binding to turn the dock off: kill the running instance and exit.
	lockFilePath := fmt.Sprintf("%s/nwg-panel-plugin-menu.lock", tempDir())
	lockFile, err := singleinstance.CreateLockFile(lockFilePath)
	if err != nil {
		pid, err := readTextFile(lockFilePath)
		if err == nil {
			i, err := strconv.Atoi(pid)
			if err == nil {
				/*if !*autohide {
					println("Running instance found, sending SIGTERM and exiting...")
					syscall.Kill(i, syscall.SIGTERM)
				} else {
					println("Already running")
				}*/
				println("Running instance found, sending SIGTERM and exiting...")
				syscall.Kill(i, syscall.SIGTERM)
			}
		}
		os.Exit(0)
	}
	defer lockFile.Close()

	// LANGUAGE
	if *lang == "" && os.Getenv("LANG") != "" {
		*lang = strings.Split(os.Getenv("LANG"), ".")[0]
	}
	println(fmt.Sprintf("lang: %s", *lang))

	// ENVIRONMENT
	configDirectory = configDir()

	cacheDirectory := cacheDir()
	if cacheDirectory == "" {
		log.Panic("Couldn't determine cache directory location")
	}

	// DATA
	pinnedFile = filepath.Join(cacheDirectory, "nwg-pin-cache")
	cssFile := filepath.Join(configDirectory, *cssFileName)

	appDirs = getAppDirs()

	setUpCategories()

	desktopFiles := listDesktopFiles()
	println(fmt.Sprintf("Found %v desktop files", len(desktopFiles)))

	parseDesktopFiles(desktopFiles)

	// USER INTERFACE
	gtk.Init(nil)

	cssProvider, _ := gtk.CssProviderNew()

	err = cssProvider.LoadFromPath(cssFile)
	if err != nil {
		println(fmt.Sprintf("ERROR: %s css file not found or erroneous. Using GTK styling.", cssFile))
		println(fmt.Sprintf(">>> %s", err))
	} else {
		println(fmt.Sprintf("Using style from %s", cssFile))
		screen, _ := gdk.ScreenGetDefault()
		gtk.AddProviderForScreen(screen, cssProvider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
	}

	win, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		log.Fatal("Unable to create window:", err)
	}

	layershell.InitForWindow(win)

	//screenWidth := 0
	screenHeight := 0

	var output2mon map[string]*gdk.Monitor
	if *targetOutput != "" {
		// We want to assign layershell to a monitor, but we only know the output name!
		output2mon, err = mapOutputs()
		if err == nil {
			monitor := output2mon[*targetOutput]
			layershell.SetMonitor(win, monitor)

			geometry := monitor.GetGeometry()
			//screenWidth = geometry.GetWidth()
			screenHeight = geometry.GetHeight()

		} else {
			println(err)
		}
	}

	if *valign == "bottom" {
		layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_BOTTOM, true)
	} else {
		layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_TOP, true)
	}

	if *halign == "left" {
		layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_LEFT, true)
	} else {
		layershell.SetAnchor(win, layershell.LAYER_SHELL_EDGE_RIGHT, true)
	}

	layershell.SetLayer(win, layershell.LAYER_SHELL_LAYER_TOP)

	layershell.SetMargin(win, layershell.LAYER_SHELL_EDGE_TOP, *marginTop)
	layershell.SetMargin(win, layershell.LAYER_SHELL_EDGE_LEFT, *marginLeft)
	layershell.SetMargin(win, layershell.LAYER_SHELL_EDGE_RIGHT, *marginRight)
	layershell.SetMargin(win, layershell.LAYER_SHELL_EDGE_BOTTOM, *marginBottom)

	layershell.SetKeyboardMode(win, layershell.LAYER_SHELL_KEYBOARD_MODE_EXCLUSIVE)

	win.Connect("destroy", func() {
		gtk.MainQuit()
	})

	win.Connect("key-release-event", func(window *gtk.Window, event *gdk.Event) {
		key := &gdk.EventKey{Event: event}
		if key.KeyVal() == gdk.KEY_Escape {
			s, _ := searchEntry.GetText()
			if s != "" {
				clearSearchResult()
				searchEntry.GrabFocus()
				searchEntry.SetText("")
			} else {
				gtk.MainQuit()
			}
		}
	})

	// Close the window on leave, but not immediately, to avoid accidental closes
	win.Connect("leave-notify-event", func() {
		if *autohide {
			src, err = glib.TimeoutAdd(uint(1000), func() bool {
				gtk.MainQuit()
				return false
			})
		}
	})

	win.Connect("enter-notify-event", func() {
		cancelClose()
	})

	win.SetProperty("name", "menu-start-window")

	outerBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	outerBox.SetProperty("name", "box")
	win.Add(outerBox)

	alignmentBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	alignmentBox.SetHomogeneous(true)
	outerBox.PackStart(alignmentBox, true, true, 10)

	leftBox, _ = gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	alignmentBox.PackStart(leftBox, true, true, 10)

	leftColumn, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	leftBox.PackStart(leftColumn, true, true, 0)

	pinnedListBox := setUpPinnedListBox()
	leftColumn.PackStart(pinnedListBox, false, false, 0)

	sep, _ := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)
	leftColumn.PackStart(sep, false, false, 10)

	categoriesListBox = setUpCategoriesListBox()
	leftColumn.PackStart(categoriesListBox, false, false, 0)

	searchEntry = setUpSearchEntry()
	leftColumn.PackEnd(searchEntry, false, false, 6)

	rightBox, _ = gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	alignmentBox.PackStart(rightBox, true, true, 10)

	rightColumn, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	rightBox.PackStart(rightColumn, true, true, 0)

	userDirsListBox = setUpUserDirsList()
	rightColumn.PackStart(userDirsListBox, false, true, 10)

	backButton = setUpBackButton()
	rightColumn.PackStart(backButton, false, false, 0)

	resultWrapper, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	rightColumn.PackStart(resultWrapper, true, true, 0)

	buttonBox := setUpButtonBox()
	rightColumn.PackEnd(buttonBox, false, true, 10)

	win.SetSizeRequest(screenHeight*6/10, screenHeight*6/10)

	win.ShowAll()
	backButton.Hide()

	pinnedListBox.UnselectAll()
	categoriesListBox.UnselectAll()
	searchEntry.GrabFocus()
	t := time.Now()
	println(fmt.Sprintf("UI created in %v ms. Thanks for watching.", t.Sub(timeStart).Milliseconds()))
	gtk.Main()
}
