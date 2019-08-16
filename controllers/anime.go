package controllers

import (
	"fmt"

	"anime/models"
	
	"github.com/astaxie/beego/orm"
)

type AnimeController struct {
	BaseController
}

func (c *AnimeController)Anime()  {
	name:=c.GetString(":name")
	m:=&models.Anime{Name:name}
	orm.NewOrm().Read(m,"name")
	fmt.Println(m)
	c.TplName="id.html"
}