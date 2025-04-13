package main

import (
	"bufio"
	"fmt"
	"github.com/magiconair/properties"
	"gopkg.in/yaml.v3"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var client = http.Client{}

const (
	URL            = "https://api.purpurmc.org/v2/purpur/%s/latest/download"
	OldestComp     = 1141 // Oldest "compound" version
	OldestSimple   = 115  // Oldest "simple" version
	OldestText     = "1.14.1"
	PropertiesFile = "server.properties"
	PurpurFile     = "purpur.yml"
)

func main() {
	reader := bufio.NewReader(os.Stdin)

	version := input(reader, "Version: ", validateVersion)
	isOffline := input(reader, "Should we run on offline mode (extremely discouraged)? ", handleBool)
	maxPlayers := input(reader, "Maximum Allowed players: ", handleNumber)
	heapSize := input(reader, "How many gigabytes should be used to the server (recommended: 2)? ", handleNumber)
	simDistance := input(reader, "How far should the server tick (Simulation Distance, recommended: 4): ", handleNumber)
	viewDistance := input(reader, "How far should players be allowed to render (View Distance [2-31], recommended: [10 - 16])? ", handleNumber)
	isHardcore := input(reader, "Should the server be on Hardcore mode? ", handleBool)
	servername := input(reader, "Server Name: ", func(s string) (string, error) { return s, nil })
	keepalive := input(reader, "Should we use Purpur's alternate keep alive (recommended)? ", handleBool)
	whitelist := input(reader, "Should we enable/enforce whitelist? ", handleBool)
	customSeed := input(reader, "You want to provide us a custom seed for world generation? ", handleBool)
	var seed int64
	if customSeed {
		seed = input(reader, "Provide your custom seed: ", handleGenSeed)
	}

	log.Println("===================================================")
	log.Printf("Downloading jar file at version \"%s\"...", version)
	jar, err := downloadJarFile(version)
	if err != nil {
		panic(err) // Sad :c
	}

	err = os.WriteFile("server.jar", jar, 0644)
	if err != nil {
		panic(err)
	}

	log.Println("Creating RUN button to start the server...")
	err = createRunButton(heapSize)
	if err != nil {
		log.Println(err)
	}

	log.Println("Generating accepted EULA file...")
	err = createEula()
	if err != nil {
		log.Println(err)
	}

	log.Println("Generating properties at \"server.properties\" file with custom provided values...")
	// Creating "server.properties" file
	err = createProperties(isOffline, maxPlayers, simDistance, viewDistance, isHardcore, servername, whitelist, seed)
	if err != nil {
		log.Println(err)
	}

	log.Println("We are going to be initializing the server in 3s to generate some configuration files, " +
		"do not kill the installer yet!")
	time.Sleep(3 * time.Second)
	// Our first attempt of running the server and stopping it right after,
	// this method will hang up the code until the server closes synchronously
	runServer()

	log.Printf("Adjusting \"%s\" file configurations...\n", PurpurFile)
	err = adjustPurpurFile(keepalive)
	if err != nil {
		log.Println(err)
	}

	log.Println("You're all done! :)")
	log.Println("Quitting in 5 seconds...")
	time.Sleep(5 * time.Second)
}

func absPath(filename string) string {
	abs, err := filepath.Abs(filename)
	if err != nil {
		panic(err)
	}
	return abs
}

func adjustPurpurFile(keepalive bool) error {
	// Default behavior
	if !keepalive {
		return nil
	}

	configFile := absPath(PurpurFile)
	data, err := os.ReadFile(configFile)
	if err != nil {
		return err
	}

	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		return err
	}

	settings, ok := config["settings"].(map[string]any)
	if !ok {
		return fmt.Errorf("could not parse %s, \"settings\" was expected to be an object\n", PurpurFile)
	}
	settings["use-alternate-keepalive"] = keepalive

	newData, err := yaml.Marshal(&config)
	if err != nil {
		return err
	}

	if err := os.WriteFile(configFile, newData, 0644); err != nil {
		return err
	}
	return nil
}

func runServer() {
	runCommand := getRunScript(2048)
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", runCommand)
	} else {
		cmd = exec.Command("bash", runCommand)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Printf("We failed to attach stdin to Minecraft Server terminal: %v", err)
		os.Exit(1)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Println("Starting Minecraft Server...")

	if err := cmd.Start(); err != nil {
		log.Printf("We failed to start Minecraft Server: %v", err)
		os.Exit(1)
	}
	stdin.Write([]byte("stop\n"))

	if err := cmd.Wait(); err != nil {
		log.Printf("It seems like the server exited with error: %v\n", err)
	}
}

func createProperties(offline bool, maxPlayers int, simDistance int, viewDistance int,
	isHardcore bool, serverName string, whitelist bool, seed int64) error {
	p := properties.NewProperties()

	setProperty(p, "online-mode", !offline)
	setProperty(p, "max-players", maxPlayers)
	setProperty(p, "simulation-distance", simDistance)
	setProperty(p, "view-distance", viewDistance)
	setProperty(p, "hardcore", isHardcore)
	setProperty(p, "server-name", serverName)
	setProperty(p, "white-list", whitelist)
	setProperty(p, "spawn-protection", 0) // This shouldn't even exist :c
	setProperty(p, "enforce-whitelist", whitelist)

	if seed != 0 {
		setProperty(p, "level-seed", seed)
	}

	file, err := os.OpenFile(absPath(PropertiesFile), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = p.Write(file, properties.UTF8)
	if err != nil {
		return err
	}
	return nil
}

func setProperty(prop *properties.Properties, name string, value any) {
	err := prop.SetValue(name, value)
	if err != nil {
		log.Printf("Failed to set '%v' property: %v\n", name, err)
	}
}

func downloadJarFile(version string) ([]byte, error) {
	url := fmt.Sprintf(URL, version)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err // ???
	}

	if resp.StatusCode == 400 {
		return nil, fail("Version \"%s\" was not found, code %d, response: %s", version, resp.StatusCode, body)
	}
	return body, nil
}

func handleGenSeed(text string) (int64, error) {
	val, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, fail("Only numbers can be provided for a seed")
	}
	return val, err
}

func handleNumber(text string) (int, error) {
	val, err := strconv.Atoi(text)
	if err != nil {
		return 0, fail("Only numbers are valid here, provided: %s", text)
	}
	return val, nil
}

func handleBool(text string) (bool, error) {
	return equals(strings.ToLower(text), "y", "yes", "true", "ok", "1"), nil
}

func equals(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}

func handleDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	err = os.MkdirAll(abs, 0755)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func validateVersion(text string) (string, error) {
	tokens := strings.Split(text, ".")
	length := len(tokens)

	if length < 2 || length > 3 {
		return "", fail("Versions can only have 2-3 tokens, ex.: 1.24 or 1.24.4")
	}

	raw := strings.ReplaceAll(text, ".", "")
	num, err := strconv.Atoi(raw)
	if err != nil {
		return "", fail("Version can only contain numbers and dots, snapshots are not allowed")
	}

	// If this is a "simple" version (like 1.15), and older than 1.15, it fails, as Purpur is 1.14.1+ only;
	// If this is a "compound" version (like 1.16.5), and is older than 1.14.1, it fails
	if (length == 2 && num < OldestSimple) || (length == 3 && num < OldestComp) {
		return "", fail("The oldest allowed version is %s", OldestText)
	}
	return text, nil
}

func fail(msg string, args ...any) error {
	if len(args) == 0 {
		return fmt.Errorf("%s\n", msg)
	} else {
		return fmt.Errorf(msg+"\n", args)
	}
}

func createRunButton(heap int) error {
	ram := heap * 1024
	cmd := getRunScript(ram) + "\nPAUSE"
	var filename string

	if runtime.GOOS == "windows" {
		filename = "run.bat"
	} else {
		filename = "run.sh"
	}

	err := os.WriteFile(absPath(filename), []byte(cmd), 0644)
	if err != nil {
		return err
	}
	return nil
}

func getRunScript(heapSize int) string {
	return fmt.Sprintf("java -Xms%dM -Xmx%dM -jar server.jar nogui", heapSize, heapSize)
}

func createEula() error {
	eula := "eula=true"
	err := os.WriteFile(absPath("eula.txt"), []byte(eula), 0644)
	if err != nil {
		return err
	}
	return nil
}

func input[T any](reader *bufio.Reader, text string, mapper func(string) (T, error)) T {
	for {
		val := readLine(reader, text)

		mapped, err := mapper(val)
		if err == nil {
			return mapped
		}
		fmt.Println(err.Error())
	}
}

func readLine(reader *bufio.Reader, text string) string {
	fmt.Print(text)
	val, err := reader.ReadString('\n')
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(val)
}
