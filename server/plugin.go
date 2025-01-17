package main

import (
	"fmt"
	"github.com/jmoiron/sqlx"
	"github.com/mattermost/mattermost-server/v6/model"
	"net/http"
	"sync"

	"github.com/mattermost/mattermost-server/v6/plugin"
)

const (
	BotUsername    = "calendar"
	BotDisplayName = "Calendar"
)

type Plugin struct {
	plugin.MattermostPlugin

	// configurationLock synchronizes access to the configuration.
	configurationLock sync.RWMutex

	// configuration is the active plugin configuration. Consult getConfiguration and
	// setConfiguration for usage.
	configuration *configuration

	DB    *sqlx.DB
	BotId string
}

func (p *Plugin) SetDB(db *sqlx.DB) {
	p.DB = db
}

func (p *Plugin) SetBotId(botId string) {
	p.BotId = botId
}

func (p *Plugin) OnActivate() error {

	config := p.API.GetUnsanitizedConfig()

	db := initDb(*config.SqlSettings.DriverName, *config.SqlSettings.DataSource)
	p.SetDB(db)

	migrator := newMigrator(db, p)
	if errMigrate := migrator.migrate(); errMigrate != nil {
		return errMigrate
	}

	if errMigrate := migrator.migrateLegacyRecurrentEvents(); errMigrate != nil {
		return errMigrate
	}

	command, err := p.createCalCommand()
	if err != nil {
		return err
	}

	if err = p.API.RegisterCommand(command); err != nil {
		return err
	}

	GetBotsResp, GetBotError := p.API.GetBots(&model.BotGetOptions{
		Page:           0,
		PerPage:        1000,
		OwnerId:        "",
		IncludeDeleted: false,
	})

	if GetBotError != nil {
		p.API.LogError(GetBotError.Error())
		return &model.AppError{
			Message:       "Can't get bot",
			DetailedError: GetBotError.Error(),
		}
	}

	botId := ""

	for _, bot := range GetBotsResp {
		if bot.Username == BotUsername {
			botId = bot.UserId
		}
	}

	if botId == "" {
		createdBot, createBotError := p.API.CreateBot(&model.Bot{
			Username:    BotUsername,
			DisplayName: BotDisplayName,
		})
		if createBotError != nil {
			p.API.LogError(createBotError.Error())
			return &model.AppError{
				Message:       "Can't create bot",
				DetailedError: createBotError.Error(),
			}
		}

		botId = createdBot.UserId

	}

	p.SetBotId(botId)

	go NewBackgroundJob(p, db).Start()
	return nil
}

func (p *Plugin) OnDeactivate() error {
	GetBackgroundJob().Done <- true

	return nil
}

// handles HTTP requests.
func (p *Plugin) ServeHTTP(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "POST":
		switch r.URL.Path {
		case "/events":
			p.CreateEvent(c, w, r)
		}
	case "GET":
		switch r.URL.Path {
		case "/events":
			p.GetEvents(c, w, r)
		case "/event":
			p.GetEvent(c, w, r)
		case "/settings":
			p.GetSettings(c, w, r)
		}
	case "DELETE":
		switch r.URL.Path {
		case "/event":
			p.RemoveEvent(c, w, r)
		}
	case "PUT":
		switch r.URL.Path {
		case "/event":
			p.UpdateEvent(c, w, r)
		case "/settings":
			p.UpdateSettings(c, w, r)
		}
	default:
		fmt.Fprint(w, "ping")
	}
}
