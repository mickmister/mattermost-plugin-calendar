package main

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/mattermost/mattermost-server/v6/model"
	"github.com/mattermost/mattermost-server/v6/plugin"
	"github.com/pkg/errors"
	"net/http"
	"time"
)

type RecurrenceItem []int

func (r *RecurrenceItem) Scan(val interface{}) error {
	switch v := val.(type) {
	case []byte:
		json.Unmarshal(v, &r)
		return nil
	case string:
		json.Unmarshal([]byte(v), &r)
		return nil
	default:
		return errors.New(fmt.Sprintf("Unsupported type: %T", v))
	}
}
func (r *RecurrenceItem) Value() (driver.Value, error) {
	return json.Marshal(r)
}

type Event struct {
	Id         string          `json:"id" db:"id"`
	Title      string          `json:"title" db:"title"`
	Start      time.Time       `json:"start" db:"start"`
	End        time.Time       `json:"end" db:"end"`
	Attendees  []string        `json:"attendees"`
	Created    time.Time       `json:"created" db:"created"`
	Owner      string          `json:"owner" db:"owner"`
	Channel    *string         `json:"channel" db:"channel"`
	Processed  *time.Time      `json:"-" db:"processed"`
	Recurrent  bool            `json:"-" db:"recurrent"`
	Recurrence *RecurrenceItem `json:"recurrence" db:"recurrence"`
}

func (p *Plugin) GetUserLocation(user *model.User) *time.Location {
	userTimeZone := ""

	if user.Timezone["useAutomaticTimezone"] == "true" {
		userTimeZone = user.Timezone["automaticTimezone"]
	} else {
		userTimeZone = user.Timezone["manualTimezone"]
	}

	userLoc, loadError := time.LoadLocation(userTimeZone)

	if loadError != nil {
		userLoc, _ = time.LoadLocation("")
	}

	return userLoc
}

func (p *Plugin) GetEvent(c *plugin.Context, w http.ResponseWriter, r *http.Request) {

	session, err := p.API.GetSession(c.SessionId)
	if err != nil {
		p.API.LogError("can't get session")
		return
	}

	user, err := p.API.GetUser(session.UserId)

	if err != nil {
		p.API.LogError("can't get user")
		return
	}

	query := r.URL.Query()

	eventId := query.Get("eventId")

	rows, errSelect := GetDb().Queryx(`SELECT ce.id,
                                              ce.title,
                                              ce."start",
                                              ce."end",
                                              ce.created,
                                              ce."owner",
                                              ce."channel",
                                              ce.recurrence,
                                              cm."user"
                                       FROM   calendar_events ce
                                              LEFT JOIN calendar_members cm
                                                      ON ce.id = cm."event"
                                       WHERE  id = $1 `, eventId)
	if errSelect != nil {
		p.API.LogError("Selecting data error")
		return
	}

	type EventFromDb struct {
		Event
		User *string `json:"user" db:"user"`
	}

	var members []string
	var eventDb EventFromDb

	for rows.Next() {
		errScan := rows.StructScan(&eventDb)

		if errScan != nil {
			p.API.LogError("Can't scan row to struct EventFromDb")
			return
		}

		if eventDb.User != nil {
			members = append(members, *eventDb.User)
		}

	}

	event := Event{
		Id:         eventDb.Id,
		Title:      eventDb.Title,
		Start:      eventDb.Start,
		End:        eventDb.End,
		Attendees:  members,
		Created:    eventDb.Created,
		Owner:      eventDb.Owner,
		Channel:    eventDb.Channel,
		Recurrence: eventDb.Recurrence,
	}

	userLoc := p.GetUserLocation(user)

	event.Start = event.Start.In(userLoc)
	event.End = event.End.In(userLoc)

	jsonBytes, _ := json.Marshal(map[string]interface{}{
		"data": &event,
	})

	w.Header().Set("Content-Type", "application/json")

	if _, errWrite := w.Write(jsonBytes); errWrite != nil {
		http.Error(w, fmt.Sprintf("Error getting dynamic args: %s", errWrite.Error()), http.StatusInternalServerError)
		return
	}

}

func (p *Plugin) GetEvents(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	session, err := p.API.GetSession(c.SessionId)

	if err != nil {
		p.API.LogError("can't get session")
		return
	}

	user, err := p.API.GetUser(session.UserId)

	if err != nil {
		p.API.LogError("can't get user")
		return
	}

	query := r.URL.Query()

	start := query.Get("start")
	end := query.Get("end")

	userLoc := p.GetUserLocation(user)
	utcLoc, _ := time.LoadLocation("UTC")

	startEventLocal, _ := time.ParseInLocation("2006-01-02T15:04:05", start, userLoc)
	EndEventLocal, _ := time.ParseInLocation("2006-01-02T15:04:05", end, userLoc)

	var events []Event

	rows, errSelect := GetDb().Queryx(`SELECT ce.id,
ce.title,
ce."start",
ce."end",
ce.created,
ce."owner",
ce."channel",
ce.recurrent,
ce.recurrence
FROM calendar_events ce
FULL JOIN calendar_members cm ON ce.id = cm."event"
WHERE (cm."user" = $1 OR ce."owner" = $2)
AND ((ce."start" >= $3 AND ce."start" <= $4) or ce.recurrent = true)
                                       `, user.Id, user.Id, startEventLocal.In(utcLoc), EndEventLocal.In(utcLoc))

	if errSelect != nil {
		p.API.LogError("Selecting data error")
		return
	}

	addedEvent := map[string]bool{}
	recurenEvents := map[int][]Event{}

	for rows.Next() {

		var eventDb Event

		errScan := rows.StructScan(&eventDb)

		if errScan != nil {
			p.API.LogError("Can't scan row to struct")
			return
		}

		eventDb.Start = eventDb.Start.In(userLoc)
		eventDb.End = eventDb.End.In(userLoc)

		if eventDb.Recurrent {
            for _, recurentDay := range *eventDb.Recurrence {
                recurenEvents[recurentDay] = append(recurenEvents[recurentDay], eventDb)
            }
			continue
		}

		if !addedEvent[eventDb.Id] && !eventDb.Recurrent {
			events = append(events, eventDb)
			addedEvent[eventDb.Id] = true
		}
	}


	currientDate := startEventLocal
	for currientDate.Before(EndEventLocal) {
        for _, ev := range recurenEvents[int(currientDate.Weekday())] {
			ev.Start = time.Date(
				currientDate.Year(),
				currientDate.Month(),
				currientDate.Day(),
				ev.Start.Hour(),
				ev.Start.Minute(),
				ev.Start.Second(),
				ev.Start.Nanosecond(),
				ev.Start.Location(),
			)
            
			ev.End = time.Date(
				currientDate.Year(),
				currientDate.Month(),
				currientDate.Day(),
				ev.End.Hour(),
				ev.End.Minute(),
				ev.End.Second(),
				ev.End.Nanosecond(),
				ev.End.Location(),
			)

            events = append(events, ev)
		}
        currientDate = currientDate.Add(time.Hour * 24)
	}

	jsonBytes, _ := json.Marshal(map[string]interface{}{
		"data": &events,
	})

	w.Header().Set("Content-Type", "application/json")

	if _, errWrite := w.Write(jsonBytes); errWrite != nil {
		http.Error(w, fmt.Sprintf("Error getting dynamic args: %s", errWrite.Error()), http.StatusInternalServerError)
		return
	}
}

func (p *Plugin) CreateEvent(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	session, err := p.API.GetSession(c.SessionId)

	if err != nil {
		p.API.LogError(err.Error())
		return
	}

	user, err := p.API.GetUser(session.UserId)

	if err != nil {
		p.API.LogError(err.Error())
		return
	}

	var event Event

	errDecode := json.NewDecoder(r.Body).Decode(&event)

	if errDecode != nil {
		p.API.LogError(errDecode.Error())
		return
	}

	event.Id = uuid.New().String()

	event.Created = time.Now().UTC()
	event.Owner = user.Id

	loc := p.GetUserLocation(user)
	utcLoc, _ := time.LoadLocation("UTC")

	startDateInLocalTimeZone := time.Date(
		event.Start.Year(),
		event.Start.Month(),
		event.Start.Day(),
		event.Start.Hour(),
		event.Start.Minute(),
		event.Start.Second(),
		event.Start.Nanosecond(),
		loc,
	)

	endDateInLocalTimeZone := time.Date(
		event.End.Year(),
		event.End.Month(),
		event.End.Day(),
		event.End.Hour(),
		event.End.Minute(),
		event.End.Second(),
		event.End.Nanosecond(),
		loc,
	)

	event.Start = startDateInLocalTimeZone.In(utcLoc)
	event.End = endDateInLocalTimeZone.In(utcLoc)

	if event.Recurrence != nil && len(*event.Recurrence) > 0 {
		event.Recurrent = true
	} else {
		event.Recurrent = false
	}

	_, errInser := GetDb().NamedExec(`INSERT INTO PUBLIC.calendar_events
                                                  (id,
                                                   title,
                                                   "start",
                                                   "end",
                                                   created,
                                                   owner,
                                                   channel,
                                                   recurrent,
                                                   recurrence)
                                      VALUES      (:id,
                                                   :title,
                                                   :start,
                                                   :end,
                                                   :created,
                                                   :owner,
                                                   :channel,
                                                   :recurrent,
                                                   :recurrence) `, &event)

	if errInser != nil {
		p.API.LogError(errInser.Error())
		return
	}

	if event.Attendees != nil {
		for _, userId := range event.Attendees {
			_, errInser = GetDb().NamedExec(`INSERT INTO public.calendar_members ("event", "user") VALUES (:event, :user)`, map[string]interface{}{
				"event": event.Id,
				"user":  userId,
			})
		}
	}

	if errInser != nil {
		p.API.LogError(errInser.Error())
		return
	}

	jsonBytes, _ := json.Marshal(map[string]interface{}{
		"data": &event,
	})

	w.Header().Set("Content-Type", "application/json")

	if _, errWrite := w.Write(jsonBytes); errWrite != nil {
		http.Error(w, fmt.Sprintf("Error getting dynamic args: %s", errWrite.Error()), http.StatusInternalServerError)
		return
	}
}

func (p *Plugin) RemoveEvent(c *plugin.Context, w http.ResponseWriter, r *http.Request) {

	_, err := p.API.GetSession(c.SessionId)

	if err != nil {
		p.API.LogError("can't get session")
		return
	}

	query := r.URL.Query()

	eventId := query.Get("eventId")

	_, dbErr := GetDb().Exec("DELETE FROM calendar_events WHERE id=$1", eventId)

	if dbErr != nil {
		p.API.LogError("can't remove event from db")
		return
	}

	jsonBytes, _ := json.Marshal(map[string]interface{}{
		"data": map[string]interface{}{
			"success": true,
		},
	})

	w.Header().Set("Content-Type", "application/json")

	if _, errWrite := w.Write(jsonBytes); errWrite != nil {
		http.Error(w, fmt.Sprintf("Error getting dynamic args: %s", errWrite.Error()), http.StatusInternalServerError)
		return
	}
}