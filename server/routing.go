package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-martini/martini"
	"github.com/martini-contrib/sessions"
	"labix.org/v2/mgo/bson"
)

func (s *AEServer) SetupRouting() {
	s.m.Get("/q", s.HandleGetQuestions)
	s.m.Post("/q", s.HandlePostQuestion)
	s.m.Get("/q/:id", s.HandleGetQuestion)
	s.m.Put("/q/:id", s.HandleEditQuestion)
	s.m.Get("/q/:id/vote/:opt", s.HandleVote)
	s.m.Post("/q/:id/response", s.HandleQuestionResponse)
	s.m.Post("/q/:id/response/:resp/comment", s.HandleResponseComment)
	s.m.Post("/q/:id/comment", s.HandleQuestionComment)

	s.m.Post("/salt", s.HandleGetSalt)
	s.m.Post("/login", s.HandleLogin)
	s.m.Post("/register", s.HandleRegister)
	s.m.Post("/register/salt", s.HandleUniqueSalt)
	s.m.Post("/logout", s.HandleLogout)
	s.m.Post("/me", s.HandleMe)
}

func (s *AEServer) HandlePostQuestion(w http.ResponseWriter, r *http.Request, session sessions.Session) (int, string) {
	//Verify user account or something
	login := session.Get("Login")
	if login == nil {
		return 404, Message("Not Logged In!")
	}
	tok := login.(string)
	user := s.syncGetUser(tok)
	if user == nil {
		return http.StatusBadRequest, Message("Invalid Cookie!")
	}

	q := QuestionFromJson(r.Body)
	if q == nil {
		return 404, Message("Poorly Formatted JSON.")
	}
	//Assign question an ID
	q.ID = bson.NewObjectId()
	q.Author = user.Username
	q.Timestamp = time.Now()

	err := s.questions.Save(q)
	if err != nil {
		log.Print(err)
		return http.StatusInternalServerError, Message("Failed to save question")
	}
	return 200, q.GetIdHex()
}

func (s *AEServer) HandleGetQuestions() (int, string) {
	q := s.questions.FindWhere(bson.M{})
	if q == nil {
		return 404, Message("Question not found.")
	}
	return 200, Stringify(q)
}
func (s *AEServer) HandleGetQuestion(params martini.Params) (int, string) {
	id := params["id"]
	hid := bson.ObjectIdHex(id)
	fmt.Println(hid)
	q, ok := s.questions.FindByID(hid).(*Question)
	if !ok || q == nil {
		return 404, ""
	}
	return 200, Stringify(q)
}

//Get salt associated with a given username
func (s *AEServer) HandleGetSalt(r *http.Request) (int, string) {
	a := AuthFromJson(r.Body)
	if a == nil || a.Username == "" {
		return 401, Message("Must send username!")
	}
	user := s.FindUserByName(a.Username)
	if user == nil {
		return 401, Message("Username not found!")
	}
	salt := genRandString()
	s.salts[a.Username] = salt
	return 200, salt
}

func (s *AEServer) HandleUniqueSalt(r *http.Request) (int, string) {
	a := AuthFromJson(r.Body)
	if a == nil || a.Username == "" {
		return 401, Message("No Username given.")
	}
	user := s.FindUserByName(a.Username)
	if user == nil {
		return 401, Message("No such user!")
	}
	return 200, fmt.Sprintf("{\"Salt\":\"%s\"}", user.Salt)
}

func (s *AEServer) HandleLogout(session sessions.Session) {
	toki := session.Get("Login")
	if toki == nil {
		return
	}
	s.ch_logout <- toki.(string)
	session.Delete("Login")
}

func (s *AEServer) HandleLogin(r *http.Request, params martini.Params, session sessions.Session) (int, string) {
	a := AuthFromJson(r.Body)
	if a == nil {
		time.Sleep(time.Second)
		return 404, Message("Login Failed")
	}

	sltr := StrResponse{}
	sltr.Arg = a.Username
	sltr.Resp = make(chan string)
	s.ch_getsalt <- sltr
	salt := <-sltr.Resp
	if salt == "" {
		return 401, Message("No login salt registered!")
	}

	user := s.FindUserByName(a.Username)
	salt_pass := DoHash(user.Password, salt)
	if salt_pass != a.Password {
		fmt.Println("Invalid password.")
		time.Sleep(time.Second)
		return http.StatusUnauthorized, Message("Invalid Username or Password.")
	}

	user.login = time.Now()
	tok := s.GetSessionToken()
	s.ch_login <- &Session{tok, user}
	session.Set("Login", tok)

	fmt.Println("Logged in!")
	return 200, Stringify(user)
}

func (s *AEServer) HandleQuestionComment(sess sessions.Session, params martini.Params, r *http.Request) (int, string) {
	id := bson.ObjectIdHex(params["id"])
	user := s.GetAuthedUser(sess)
	if user == nil {
		return 401, Message("Not authorized to reply!")
	}

	comment := user.MakeComment(r.Body)
	if comment == nil {
		return http.StatusBadRequest, Message("Poorly formatted JSON")
	}

	question, ok := s.questions.FindByID(id).(*Question)
	if !ok {
		return http.StatusForbidden, Message("No such question!")
	}
	question.AddComment(comment)
	s.questions.Update(question)

	return 200, string(comment.JsonBytes())
}

func (s *AEServer) HandleEditQuestion(sess sessions.Session, params martini.Params, r *http.Request) (int, string) {
	id := bson.ObjectIdHex(params["id"])
	user := s.GetAuthedUser(sess)
	if user == nil {
		return 401, Message("Not authorized to edit!")
	}

	q := QuestionFromJson(r.Body)
	if q == nil {
		return http.StatusBadRequest, Message("Poorly formatted JSON")
	}

	original := s.questions.FindByID(id).(*Question)
	original.Body = q.Body
	original.Title = q.Title
	original.LastEdit = time.Now()

	s.questions.Update(original)
	return 200, Stringify(original)
}

func (s *AEServer) HandleQuestionResponse(sess sessions.Session, params martini.Params, r *http.Request) (int, string) {
	id := bson.ObjectIdHex(params["id"])
	user := s.GetAuthedUser(sess)
	if user == nil {
		return 401, Message("Not authorized to reply!")
	}

	resp := user.MakeRespose(r.Body)
	if resp == nil {
		return http.StatusBadRequest, Message("Poorly formatted JSON")
	}

	question, ok := s.questions.FindByID(id).(*Question)
	if !ok {
		return http.StatusForbidden, Message("No such question!")
	}
	question.AddResponse(resp)
	s.questions.Update(question)

	return 200, Stringify(resp)
}

func (s *AEServer) HandleResponseComment(sess sessions.Session, params martini.Params, r *http.Request) (int, string) {
	id := bson.ObjectIdHex(params["id"])
	user := s.GetAuthedUser(sess)
	if user == nil {
		return 401, Message("Not authorized to reply!")
	}

	comment := user.MakeComment(r.Body)
	if comment == nil {
		return http.StatusBadRequest, Message("Poorly formatted JSON")
	}

	question, ok := s.questions.FindByID(id).(*Question)
	if !ok {
		return http.StatusForbidden, Message("No such question!")
	}
	resp_id := params["resp"]
	resp := question.GetResponse(bson.ObjectId(resp_id))
	resp.AddComment(comment)

	s.questions.Update(question)

	return 200, string(comment.JsonBytes())
}

func (s *AEServer) HandleMe(session sessions.Session) (int, string) {
	return 200, Message("Nothing here")
}

func (s *AEServer) HandleVote(params martini.Params, session sessions.Session, r *http.Request) (int, string) {
	opt := params["opt"]
	if opt != "up" && opt != "down" {
		return http.StatusMethodNotAllowed, Message("Invalid vote type")
	}
	user := s.GetAuthedUser(session)
	if user == nil {
		return http.StatusUnauthorized, Message("Not logged in!")
	}

	q := bson.ObjectIdHex(params["id"])
	question, ok := s.questions.FindByID(q).(*Question)
	if question == nil || !ok {
		return 404, Message("No such question!")
	}
	switch opt {
	case "up":
		if question.Upvote(user.ID) {
			s.questions.Update(question)
		}
	case "down":
		if question.Downvote(user.ID) {
			s.questions.Update(question)
		}
	}

	return 200, Stringify(question)
}

func (s *AEServer) HandleRegister(r *http.Request) (int, string) {
	var a AuthAttempt
	dec := json.NewDecoder(r.Body)
	err := dec.Decode(&a)
	if err != nil {
		fmt.Println(err)
		return 404, Message("Register Failed")
	}

	fmt.Println("Registering new user!")
	fmt.Println(a)
	user := new(User)
	user.Password = a.Password
	user.Username = a.Username
	user.Salt = a.Salt
	user.ID = bson.NewObjectId()

	err = s.users.Save(user)
	if err != nil {
		fmt.Println(err)
	}
	return 200, Message("Success!")
}
