package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// TODO: Move these functions to another module
func StringInSlice(s string, list []string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func AnyKeyInMap(keys []string, keyMap map[string]interface{}) bool {
	for k := range keyMap {
		if StringInSlice(k, keys) {
			return true
		}
	}
	return false
}

// Bot parameters
var (
	GuildID        = flag.String("guild", "", "Test guild ID. If not passed - bot registers commands globally")
	BotToken       = flag.String("token", "", "Bot access token")
	RemoveCommands = flag.Bool("rmcmd", true, "Remove all commands when shutting down")
)

var s *discordgo.Session

func init() { flag.Parse() }

func init() {
	var err error
	s, err = discordgo.New("Bot " + *BotToken)
	if err != nil {
		log.Fatalf("Invalid bot parameters: %v", err)
	}
}

var (
	minMemberIdValue = 1.0
	dmPermission     = false

	commands = []*discordgo.ApplicationCommand{
		{
			Name: "ping",
			// All commands and options must have a description
			// Commands/options without description will fail the registration
			// of the command.
			Description: "Ping Pong Test!",
		},
		{
			Name:        "elo",
			Description: "Retrieve a player's ELO, at least 1 option is required",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "firstname",
					Description: "Prenom/First name",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "lastname",
					Description: "Nom/Last name",
					Required:    false,
				}, {
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "id",
					Description: "Matricule/ID FQE",
					MinValue:    &minMemberIdValue,
					Required:    false,
				},
			},
		},
	}

	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"ping": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "Pong!",
				},
			})
		},
		"elo": eloCommandHandler,
	}
	fqeMemberListRegex = regexp.MustCompile(`<a href="index\.php\?Id=(\d+)">(.*?)<\/a>`)
)

func eloCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	var playerId int
	var playerFirstName string
	var playerLastName string
	// Access options in the order provided by the user.
	options := i.ApplicationCommandData().Options

	// Or convert the slice into a map
	optionMap := make(map[string]*discordgo.ApplicationCommandInteractionDataOption, len(options))
	for _, opt := range options {
		optionMap[opt.Name] = opt
	}

	// This example stores the provided arguments in an []interface{}
	// which will be used to format the bot's response
	margs := make([]interface{}, 0, len(options))

	// Get the value from the option map.
	// When the option exists, ok = true
	if opt, ok := optionMap["firstname"]; ok {
		playerFirstName = opt.StringValue()
		margs = append(margs, playerFirstName)
	}

	if opt, ok := optionMap["lastname"]; ok {
		playerLastName = opt.StringValue()
		margs = append(margs, playerLastName)
	}

	if opt, ok := optionMap["id"]; ok {
		playerId = int(opt.IntValue())
		margs = append(margs, playerId)
	}

	var response string

	if len(margs) == 0 {
		response = "At least one option is required!"
	} else {
		foundPlayers, err := searchFqeMember(playerFirstName, playerLastName, playerId)

		if err != nil {
			response = fmt.Sprintln("Failed to search for player", err)
		} else if len(foundPlayers) == 0 {
			response = "No players found! :("
		} else if len(foundPlayers) == 1 {
			playerDetails := foundPlayers[0]

			player, err := getFqePlayerRating(playerDetails.ID)
			var playerEloString string
			if err != nil {
				playerEloString = fmt.Sprintln("Error getting player ELO info:", err)
			} else {
				playerEloString = stringifyPlayer(player)
			}

			response = response + fmt.Sprintf("\nName: %s\nID: %d\n\nFQE rating:\n%s",
				playerDetails.Name, playerDetails.ID, playerEloString)
		} else {
			var playerStrings []string
			for _, player := range foundPlayers {
				playerStrings = append(playerStrings, fmt.Sprintf("%s, %d", player.Name, player.ID))
			}

			response = "Found players:\n" + strings.Join(playerStrings, "\n")
		}
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: response},
	})
}

func init() {
	s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if h, ok := commandHandlers[i.ApplicationCommandData().Name]; ok {
			h(s, i)
		}
	})
}

func main() {
	s.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("Logged in as: %v#%v", s.State.User.Username, s.State.User.Discriminator)
	})
	err := s.Open()
	if err != nil {
		log.Fatalf("Cannot open the session: %v", err)
	}

	log.Println("Adding commands...")
	registeredCommands := make([]*discordgo.ApplicationCommand, len(commands))
	for i, v := range commands {
		cmd, err := s.ApplicationCommandCreate(s.State.User.ID, *GuildID, v)
		if err != nil {
			log.Panicf("Cannot create '%v' command: %v", v.Name, err)
		}
		registeredCommands[i] = cmd
	}

	defer s.Close()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	log.Println("Press Ctrl+C to exit")
	<-stop

	if *RemoveCommands {
		log.Println("Removing commands...")
		// // We need to fetch the commands, since deleting requires the command ID.
		// // We are doing this from the returned commands on line 375, because using
		// // this will delete all the commands, which might not be desirable, so we
		// // are deleting only the commands that we added.
		// registeredCommands, err := s.ApplicationCommands(s.State.User.ID, *GuildID)
		// if err != nil {
		// 	log.Fatalf("Could not fetch registered commands: %v", err)
		// }

		for _, v := range registeredCommands {
			err := s.ApplicationCommandDelete(s.State.User.ID, *GuildID, v.ID)
			if err != nil {
				log.Panicf("Cannot delete '%v' command: %v", v.Name, err)
			}
		}
	}

	log.Println("Gracefully shutting down.")
}

type Player struct {
	FirstName string
	LastName  string
	ID        int
	Elos      map[string][]Elo
}

type Elo struct {
	Date  string `json:"Quand"`
	Value int    `json:"Cote"`
}

func NewPlayer() Player {
	return Player{Elos: make(map[string][]Elo)}
}

func getFqePlayerRating(id int) (Player, error) {
	player := NewPlayer()
	player.ID = id

	for i, tc := range []string{"Lente", "Semi-rapide", "Rapide"} {
		reqUrl := fmt.Sprintf("https://www.fqechecs.qc.ca/membres/json-cote.php?id=%d&c=%d", id, i+1)
		resp, err := http.Get(reqUrl)
		if err != nil {
			return player, errors.New("Request for FQE player ELO failed! Error with request.")
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return player, errors.New("Request for FQE player ELO failed! Error reading response.")
		}

		var elos []Elo
		// Parse []byte to go struct pointer
		if err := json.Unmarshal(body, &elos); err != nil {
			continue
		}

		player.Elos[tc] = elos
	}
	return player, nil
}

func stringifyPlayer(player Player) string {
	var eloStrings []string

	if len(player.Elos) == 0 {
		return "Player is either invalid or has no ELOs"
	}

	for tc, elos := range player.Elos {
		// If player doesn't have a ELO in this TC
		var eloString string
		if len(elos) == 0 {
			eloString = fmt.Sprintf("%s: ?", tc)
		} else {
			lastElo := elos[len(elos)-1]
			eloString = fmt.Sprintf("%s: %d (%s)", tc, lastElo.Value, lastElo.Date)
		}
		eloStrings = append(eloStrings, eloString)
	}
	return strings.Join(eloStrings[:], "\n")
}

type PlayerSearchResult struct {
	Name string
	ID   int
}

func searchFqeMember(firstname string, lastname string, id int) ([]PlayerSearchResult, error) {
	var res []PlayerSearchResult

	body := new(bytes.Buffer)
	mp := multipart.NewWriter(body)

	if firstname != "" {
		mp.WriteField("FName", firstname)
	}
	if lastname != "" {
		mp.WriteField("Name", lastname)
	}
	if id != 0 {
		mp.WriteField("Matricule", fmt.Sprint(id))
	}
	ct := mp.FormDataContentType()
	mp.Close()

	resp, err := http.Post("https://www.fqechecs.qc.ca/membres/list-membres.php?c=Qui", ct, body)

	if err != nil {
		return res, errors.New("Request failed")
	}

	respBody, err := io.ReadAll(resp.Body)

	if err != nil {
		return res, errors.New("Error parsing response body")
	}

	matches := fqeMemberListRegex.FindAllStringSubmatch(string(respBody), -1)
	for _, v := range matches {
		id, err := strconv.ParseInt(v[1], 10, 32)
		if err != nil {
			return res, errors.New("Invalid FQE ID")
		}
		res = append(res, PlayerSearchResult{Name: v[2], ID: int(id)})
	}

	return res, nil
}
